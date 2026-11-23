package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/httputil"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
	"github.com/spf13/cobra"
)

var callCmd = &cobra.Command{
	Use:   "call <api> [param=value ...]",
	Short: "Call an API using Luxo binary protocol / 使用 Luxo 二进制协议调用 API",
	Long: `Send a request to a running Luxo server using the binary protocol.
使用二进制协议向运行中的 Luxo 服务发送请求。

Examples / 示例:
  luxo call getUser id=1
  luxo call listTasks projectId=1 --json
  luxo call register username=alice email=alice@test.com password=secret`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCall,
}

var (
	callHost             string
	callJSON             bool
	callSelect           string
	callTimeout          time.Duration
	callResponseMaxBytes int64
)

func init() {
	callCmd.Flags().StringVar(&callHost, "host", "http://localhost:8080", "server URL")
	callCmd.Flags().BoolVar(&callJSON, "json", false, "use JSON mode instead of binary")
	callCmd.Flags().StringVar(&callSelect, "select", "", "field selection (comma-separated)")
	callCmd.Flags().DurationVar(&callTimeout, "timeout", httputil.DefaultTimeout, "total request timeout")
	callCmd.Flags().Int64Var(&callResponseMaxBytes, "max-response-bytes", httputil.DefaultResponseBodyLimit, "maximum decoded response body size in bytes")
	rootCmd.AddCommand(callCmd)
}

func runCall(cmd *cobra.Command, args []string) error {
	if callTimeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	if callResponseMaxBytes <= 0 {
		return fmt.Errorf("--max-response-bytes must be positive")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), callTimeout)
	defer cancel()
	apiName := args[0]

	if callJSON {
		return callJSONMode(ctx, apiName, args[1:])
	}
	return callBinaryMode(ctx, apiName, args[1:])
}

// The private body contains only strings or independently copied, validated RawMessages.
// Keep encoder error propagation; arbitrary marshalers, cycles and non-finite floats cannot enter here.
func callJSONMode(ctx context.Context, apiName string, params []string) error {
	body := map[string]any{}
	if callSelect != "" {
		body["$select"] = callSelect
	}
	for _, p := range params {
		k, v := parseParam(p)
		body[k] = inferType(v)
	}
	body["$api"] = apiName

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode JSON request: %w", err)
	}
	resp, err := sendCLICall(ctx, data, false)
	if err != nil {
		return err
	}
	return printCLIHTTPJSON(resp.body, resp.status)
}

func callBinaryMode(ctx context.Context, apiName string, params []string) error {
	runtimeSchema, err := loadCallSchema("luxo.schema.json")
	if err != nil {
		return err
	}
	apiSchema := runtimeSchema.APIs[apiName]
	if apiSchema == nil {
		return fmt.Errorf("unknown API %q (not in luxo.schema.json)", apiName)
	}

	paramMap := make(map[string]any)
	paramMeta := make([]api.ParamMeta, 0, len(apiSchema.Params))
	paramsByName := make(map[string]schema.Param, len(apiSchema.Params))
	for _, param := range apiSchema.Params {
		paramsByName[param.Name] = param
		paramMeta = append(paramMeta, api.ParamMeta{
			Name: param.Name, Type: param.Type.String(), FieldID: param.ID, IsList: param.IsList, Nullable: param.Nullable,
		})
	}
	for _, p := range params {
		k, v := parseParam(p)
		param, ok := paramsByName[k]
		if !ok {
			return fmt.Errorf("unknown param %q for API %q", k, apiName)
		}
		value, err := parseBinaryCLIValue(v, param)
		if err != nil {
			return fmt.Errorf("param %s: %w", k, err)
		}
		paramMap[k] = value
	}

	fieldMask, err := callFieldMask(callSelect, apiSchema, runtimeSchema)
	if err != nil {
		return err
	}
	body, err := api.EncodeBinaryRequest(apiSchema.ID, fieldMask, paramMap, paramMeta)
	if err != nil {
		return fmt.Errorf("encode binary request: %w", err)
	}

	resp, err := sendCLICall(ctx, body, true)
	if err != nil {
		return err
	}
	if !resp.binary {
		// JSON response (error or fallback).
		return printCLIHTTPJSON(resp.body, resp.status)
	}
	fmt.Fprintf(os.Stderr, "[binary] %d bytes, status %d\n", len(resp.body), resp.status)
	if resp.status >= http.StatusOK && resp.status < http.StatusMultipleChoices {
		return decodeBinaryResponse(resp.body, apiSchema, runtimeSchema)
	}
	binaryErr, err := api.DecodeBinaryError(resp.body, resp.status)
	if err != nil {
		return err
	}
	return binaryErr
}

type cliCallResponse struct {
	body   []byte
	status int
	binary bool
}

func sendCLICall(ctx context.Context, body []byte, binary bool) (cliCallResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callHost+"/luvia", bytes.NewReader(body))
	if err != nil {
		return cliCallResponse{}, fmt.Errorf("request failed: %w", err)
	}
	if binary {
		req.Header.Set("X-Luxo-Mode", "binary")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cliCallResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := httputil.ReadResponseBody(resp.Body, callResponseMaxBytes)
	if err != nil {
		return cliCallResponse{}, err
	}
	return cliCallResponse{body: data, status: resp.StatusCode, binary: binary && resp.Header.Get("X-Luxo-Mode") == "binary"}, nil
}

func printCLIHTTPJSON(body []byte, status int) error {
	fmt.Println(string(body))
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return cliHTTPStatusError(status)
	}
	return nil
}

func cliHTTPStatusError(status int) error {
	const prefix = "server returned HTTP status "
	// Every int64 decimal fits, without boxing the status or delaying Error().
	var message [len(prefix) + 20]byte
	data := append(message[:0], prefix...)
	data = strconv.AppendInt(data, int64(status), 10)
	return errors.New(string(data))
}

// decodeBinaryResponse prints a hex + field dump of a binary response.
func decodeBinaryResponse(data []byte, apiSchema *schema.API, runtimeSchema *schema.Schema) error {
	if len(data) == 0 && apiSchema.ReturnType == "" {
		fmt.Println("(empty response)")
		return nil
	}

	if apiSchema.ReturnType != "" {
		if apiSchema.ReturnNullable {
			value, null, err := codec.UnwrapNullableRoot(data)
			if err != nil {
				return err
			}
			if null {
				fmt.Println("null")
				return nil
			}
			data = value
		}
		if model := runtimeSchema.LookupModel(apiSchema.ReturnType); model != nil {
			var output []byte
			var err error
			switch {
			case apiSchema.Paginated:
				output, err = schema.BinaryPaginatedListToJSON(nil, data, model, runtimeSchema)
			case apiSchema.ReturnList:
				output, err = schema.BinaryListToJSON(nil, data, model, runtimeSchema)
			default:
				output, err = schema.BinaryToJSON(nil, data, model, runtimeSchema)
			}
			if err != nil {
				return err
			}
			fmt.Println(string(output))
			return nil
		}
		output, err := decodeBinaryScalarResponse(data, apiSchema, runtimeSchema)
		if err != nil {
			return err
		}
		fmt.Println(string(output))
		return nil
	}

	// Hex dump
	fmt.Printf("  hex: ")
	for i, b := range data {
		if i > 0 && i%16 == 0 {
			fmt.Printf("\n       ")
		}
		fmt.Printf("%02x ", b)
	}
	fmt.Println()
	return nil
}

func decodeBinaryScalarResponse(data []byte, meta *schema.API, runtimeSchema *schema.Schema) ([]byte, error) {
	wireType := meta.ReturnType
	if runtimeSchema.Enums[wireType] != nil {
		wireType = "String"
	}
	if meta.ReturnList {
		return schema.BinaryScalarListToJSON(nil, data, wireType)
	}
	return schema.BinaryScalarToJSON(nil, data, wireType)
}

func loadCallSchema(path string) (*schema.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w (run luxo gen first)", path, err)
	}
	var result schema.Schema
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := result.Prepare(); err != nil {
		return nil, fmt.Errorf("prepare %s: %w", path, err)
	}
	return &result, nil
}

func callFieldMask(selectText string, apiSchema *schema.API, runtimeSchema *schema.Schema) ([]byte, error) {
	if strings.TrimSpace(selectText) == "" {
		return nil, nil
	}
	model := runtimeSchema.LookupModel(apiSchema.ReturnType)
	if model == nil {
		return nil, fmt.Errorf("API %s does not return a selectable model", apiSchema.Name)
	}
	fields, err := selection.Parse(selectText)
	if err != nil {
		return nil, fmt.Errorf("parse --select: %w", err)
	}
	return schema.SelectToFieldMask(fields, model, runtimeSchema)
}
