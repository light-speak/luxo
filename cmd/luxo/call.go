package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

	data, _ := json.Marshal(body)
	resp, err := http.Post(callHost+"/luvia", "application/json", strings.NewReader(string(data)))
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

	req, err := http.NewRequest("POST", callHost+"/luvia", strings.NewReader(string(body)))
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
		decodeBinaryResponse(respBody, apiSchema, runtimeSchema)
	} else {
		// JSON response (error or fallback)
		fmt.Println(string(respBody))
	}
	return nil
}

// decodeBinaryResponse prints a hex + field dump of a binary response.
func decodeBinaryResponse(data []byte, apiSchema *schema.API, runtimeSchema *schema.Schema) {
	if len(data) == 0 {
		fmt.Println("(empty response)")
		return
	}

	if apiSchema.ReturnType != "" {
		if model := responseModel(apiSchema.ReturnType, runtimeSchema); model != nil {
			var output []byte
			switch {
			case apiSchema.Paginated:
				output = schema.BinaryPaginatedListToJSON(nil, data, model, runtimeSchema)
			case apiSchema.ReturnList:
				output = schema.BinaryListToJSON(nil, data, model, runtimeSchema)
			default:
				output = schema.BinaryToJSON(nil, data, model, runtimeSchema)
			}
			fmt.Println(string(output))
			return
		}
		fmt.Println(string(schema.BinaryScalarToJSON(nil, data, apiSchema.ReturnType)))
		return
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
	for _, model := range result.Models {
		result.RegisterModel(model)
	}
	return &result, nil
}

func responseModel(typeName string, runtimeSchema *schema.Schema) *schema.Model {
	if model := runtimeSchema.Models[typeName]; model != nil {
		return model
	}
	if decl := runtimeSchema.Types[typeName]; decl != nil {
		return decl.AsModel()
	}
	return nil
}

func callFieldMask(selectText string, apiSchema *schema.API, runtimeSchema *schema.Schema) ([]byte, error) {
	if strings.TrimSpace(selectText) == "" {
		return nil, nil
	}
	model := responseModel(apiSchema.ReturnType, runtimeSchema)
	if model == nil {
		return nil, fmt.Errorf("API %s does not return a selectable model", apiSchema.Name)
	}
	fields, err := selection.Parse(selectText)
	if err != nil {
		return nil, fmt.Errorf("parse --select: %w", err)
	}
	return schema.SelectToFieldMask(fields, model, runtimeSchema)
}

func parseBinaryCLIValue(raw string, param schema.Param) (any, error) {
	if param.IsList {
		var values []any
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, fmt.Errorf("expected JSON array: %w", err)
		}
		for i := range values {
			value, err := parseBinaryJSONValue(values[i], param.Type)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			values[i] = value
		}
		return values, nil
	}
	if param.Type == schema.FieldJSON {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("expected JSON value: %w", err)
		}
		return value, nil
	}
	return parseBinaryTextValue(raw, param.Type)
}

func parseBinaryJSONValue(value any, fieldType schema.FieldType) (any, error) {
	if fieldType == schema.FieldJSON {
		return value, nil
	}
	if fieldType == schema.FieldBytes {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected base64 string")
		}
		return parseBinaryTextValue(text, fieldType)
	}
	if text, ok := value.(string); ok {
		return parseBinaryTextValue(text, fieldType)
	}
	return value, nil
}

func parseBinaryTextValue(raw string, fieldType schema.FieldType) (any, error) {
	switch fieldType {
	case schema.FieldInt, schema.FieldDuration:
		return strconv.ParseInt(raw, 10, 64)
	case schema.FieldFloat:
		return strconv.ParseFloat(raw, 64)
	case schema.FieldBool:
		return strconv.ParseBool(raw)
	case schema.FieldBytes:
		return base64.StdEncoding.DecodeString(raw)
	case schema.FieldString, schema.FieldEnum, schema.FieldDateTime, schema.FieldUUID, schema.FieldDecimal:
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported CLI type %s", fieldType.String())
	}
}

func parseParam(s string) (string, string) {
	idx := strings.IndexByte(s, '=')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

func inferType(v string) any {
	// Try int
	var i int64
	if _, err := fmt.Sscanf(v, "%d", &i); err == nil && fmt.Sprintf("%d", i) == v {
		return i
	}
	// Try float
	var f float64
	if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
		return f
	}
	// Try bool
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	return v
}
