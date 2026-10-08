package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/api"
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
	callHost   string
	callJSON   bool
	callSelect string
)

func init() {
	callCmd.Flags().StringVar(&callHost, "host", "http://localhost:8080", "server URL")
	callCmd.Flags().BoolVar(&callJSON, "json", false, "use JSON mode instead of binary")
	callCmd.Flags().StringVar(&callSelect, "select", "", "field selection (comma-separated)")
	rootCmd.AddCommand(callCmd)
}

func runCall(cmd *cobra.Command, args []string) error {
	apiName := args[0]

	if callJSON {
		return callJSONMode(apiName, args[1:])
	}
	return callBinaryMode(apiName, args[1:])
}

func callJSONMode(apiName string, params []string) error {
	body := map[string]any{"$api": apiName}
	if callSelect != "" {
		body["$select"] = callSelect
	}
	for _, p := range params {
		k, v := parseParam(p)
		body[k] = inferType(v)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode JSON request: %w", err)
	}
	resp, err := http.Post(callHost+"/luvia", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println(string(respBody))
	return nil
}

func callBinaryMode(apiName string, params []string) error {
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

	req, err := http.NewRequest("POST", callHost+"/luvia", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Luxo-Mode", "binary")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.Header.Get("X-Luxo-Mode") == "binary" {
		fmt.Fprintf(os.Stderr, "[binary] %d bytes, status %d\n", len(respBody), resp.StatusCode)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			binaryErr, decodeErr := api.DecodeBinaryError(respBody, resp.StatusCode)
			if decodeErr != nil {
				return decodeErr
			}
			return binaryErr
		}
		return decodeBinaryResponse(respBody, apiSchema, runtimeSchema)
	} else {
		// JSON response (error or fallback)
		fmt.Println(string(respBody))
	}
	return nil
}

// decodeBinaryResponse prints a hex + field dump of a binary response.
func decodeBinaryResponse(data []byte, apiSchema *schema.API, runtimeSchema *schema.Schema) error {
	if len(data) == 0 && apiSchema.ReturnType == "" {
		fmt.Println("(empty response)")
		return nil
	}

	if apiSchema.ReturnType != "" {
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
