package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
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
	// Load luxo.lock for API IDs
	lf, err := lockfile.Load("luxo.lock")
	if err != nil {
		return fmt.Errorf("load luxo.lock: %w (run from project root)", err)
	}

	apiID := lf.APIID(apiName)
	if apiID == 0 {
		return fmt.Errorf("unknown API %q (not in luxo.lock)", apiName)
	}

	// Build binary request — use param field IDs from luxo.lock
	paramMap := make(map[string]any)
	var paramMeta []api.ParamMeta
	for _, p := range params {
		k, v := parseParam(p)
		paramMap[k] = inferType(v)
		fid := lf.APIParamID(apiName, k)
		if fid == 0 {
			return fmt.Errorf("unknown param %q for API %q (not in luxo.lock)", k, apiName)
		}
		paramMeta = append(paramMeta, api.ParamMeta{
			Name:    k,
			Type:    inferLuxoType(v),
			FieldID: fid,
		})
	}

	body := api.EncodeBinaryRequest(apiID, paramMap, paramMeta)

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
		// Decode binary response
		fmt.Fprintf(os.Stderr, "[binary] %d bytes, status %d\n", len(respBody), resp.StatusCode)
		decodeBinaryResponse(respBody)
	} else {
		// JSON response (error or fallback)
		fmt.Println(string(respBody))
	}
	return nil
}

// decodeBinaryResponse prints a hex + field dump of a binary response.
func decodeBinaryResponse(data []byte) {
	if len(data) == 0 {
		fmt.Println("(empty response)")
		return
	}

	dec := codec.NewDecoder(data)
	for dec.NextField() {
		fid := dec.FieldID()
		// Try reading as different types — peek at raw bytes
		// For now, just show field IDs and raw hex
		fmt.Printf("  field %d: (raw bytes follow)\n", fid)
		// We can't determine the type without schema, so just skip
		break
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

func inferLuxoType(v string) string {
	var i int64
	if _, err := fmt.Sscanf(v, "%d", &i); err == nil && fmt.Sprintf("%d", i) == v {
		return "Int"
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
		return "Float"
	}
	if v == "true" || v == "false" {
		return "Boolean"
	}
	return "String"
}
