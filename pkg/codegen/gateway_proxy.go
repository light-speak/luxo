package codegen

import "strings"

func writeGatewayProxy(b *strings.Builder) {
	b.WriteString(gatewayProxyHandler)
	b.WriteString(gatewayProxyPreflight)
	b.WriteString(gatewayProxyMerge)
	b.WriteString(gatewayProxyKeys)
	b.WriteString(gatewayProxyResolve)
	b.WriteString(gatewayProxyParallel)
	b.WriteString(gatewayProxyField)
	b.WriteString(gatewayProxyOwner)
	b.WriteString(gatewayProxySingleTarget)
}

const gatewayProxyHandler = `
// proxyHandler forwards requests to a backend service via Luxo RPC.
// Cross-service fields are resolved completely before publishing a response.
func proxyHandler(client *rpc.Client, s *schema.Schema, apiName string, rpcClients map[string]*rpc.Client) api.HandlerFunc {
	apiMeta := s.APIs[apiName]
	var model *schema.Model
	if apiMeta != nil { model = s.Models[apiMeta.ReturnType] }
	return func(ctx context.Context, req *api.Request) error {
		if client == nil || apiMeta == nil { return fmt.Errorf("gateway: missing primary service or API %s", apiName) }
		bearerToken := luvia.BearerToken(ctx)
		// Parameters were validated and encoded once at the protocol edge.
		params := req.BinaryParams()
		trace := api.DebugTrace(ctx)
		_, planSpan := trace.StartSpan(ctx, api.DebugSpanMeta{Name: "query.plan", Category: "gateway", Service: "gateway", Operation: apiName})
		plan, err := luvia.Plan(model, req.FieldMask, apiMeta.Module)
		trace.FinishSpan(planSpan)
		if err != nil { return err }
		mask := req.FieldMask
		if plan != nil {
			if err := proxyPreflight(s, plan, rpcClients); err != nil { return err }
			mask = plan.Primary.Mask
		}
		resp, err := client.CallWithMaskTargetContext(ctx, bearerToken, apiMeta.ID, mask, params, rpc.TraceTarget{Operation: apiName, Dependency: api.TraceDependencySequential})
		if err != nil { return err }
		dst := req.Buf.B
		if apiMeta.ReturnNullable {
			value, null, err := codec.UnwrapNullableRoot(resp)
			if err != nil { return err }
			if null { req.Buf.B = codec.AppendNull(dst); return nil }
			resp = value
			dst = codec.AppendPresent(dst)
		}
		if plan != nil {
			merged, err := proxyMerge(ctx, s, model, apiMeta, plan, dst, resp, rpcClients)
			if err != nil { return err }
			req.Buf.B = merged
			return nil
		}
		req.Buf.B = append(dst, resp...)
		return nil
	}
}
`

const gatewayProxyPreflight = `
func proxyPreflight(s *schema.Schema, plan *luvia.QueryPlan, clients map[string]*rpc.Client) error {
	for _, ext := range plan.Extends {
		if clients[ext.Module] == nil { return fmt.Errorf("gateway: field %s requires service %s", ext.FieldName, ext.Module) }
		if s.APIs[ext.SvcName] == nil { return fmt.Errorf("gateway: field %s requires resolver %s in service %s", ext.FieldName, ext.SvcName, ext.Module) }
	}
	return nil
}
`

const gatewayProxyMerge = `
func proxyMerge(ctx context.Context, s *schema.Schema, model *schema.Model, apiMeta *schema.API, plan *luvia.QueryPlan, dst, primary []byte, clients map[string]*rpc.Client) ([]byte, error) {
	keys, err := proxyPrimaryKeys(primary, apiMeta.ReturnList, plan, model, s)
	if err != nil { return nil, err }
	if keys.Len() == 0 { return append(dst, primary...), nil }
	columns, err := proxyResolve(ctx, s, plan.Extends, keys, clients)
	if err != nil { return nil, err }
	trace := api.DebugTrace(ctx)
	_, mergeSpan := trace.StartSpan(ctx, api.DebugSpanMeta{Name: "response.merge", Category: "gateway", Service: "gateway", Operation: apiMeta.Name})
	defer trace.FinishSpan(mergeSpan)
	// A single row has one blob per field; the canonical field bytes are identical.
	return luvia.AppendMergedColumnar(dst, primary, columns), nil
}
`

const gatewayProxyKeys = `
func proxyPrimaryKeys(primary []byte, list bool, plan *luvia.QueryPlan, model *schema.Model, s *schema.Schema) (luvia.BinaryKeys, error) {
	var keys luvia.BinaryKeys
	var ok bool
	if list {
		keys, ok = luvia.ExtractPrimaryKeyColumn(primary, plan.PrimaryKeyField, model)
		if !ok {
			reader := codec.NewColumnarReader(primary)
			// Only a complete empty list is a successful response without keys.
			ok = reader.Err() == nil && reader.Count() == 0 && reader.Offset() == len(primary)-1 && primary[len(primary)-1] == 0
		}
	} else {
		keys, ok = luvia.ExtractPrimaryKeys(primary, plan.PrimaryKeyField, model, s)
	}
	if !ok { return luvia.BinaryKeys{}, fmt.Errorf("gateway: invalid or missing primary key in %s response", model.Name) }
	return keys, nil
}

func proxyResolveParams(steps []luvia.ExtendStep, keys luvia.BinaryKeys) [4][]byte {
	var params [4][]byte
	for _, step := range steps {
		shape := step.Shape()
		if params[shape] == nil { params[shape] = keys.EncodeResolveParams(shape) }
	}
	return params
}
`

const gatewayProxyResolve = `
func proxyResolve(ctx context.Context, s *schema.Schema, steps []luvia.ExtendStep, keys luvia.BinaryKeys, clients map[string]*rpc.Client) ([]luvia.ExtendColumnResult, error) {
	params := proxyResolveParams(steps, keys)
	columns := make([]luvia.ExtendColumnResult, len(steps))
	if proxySingleTarget(steps) {
		column, err := proxyResolveField(ctx, s, steps[0], keys.Len(), params, clients, api.TraceDependencySequential)
		if err != nil { return nil, err }
		for i, step := range steps {
			columns[i] = column
			columns[i].FieldID = step.FieldID
		}
		return columns, nil
	}
	if err := proxyResolveParallel(ctx, s, steps, keys.Len(), params, clients, columns); err != nil { return nil, err }
	// Identical endpoint, cardinality and selection may reuse the same RPC.
	// Different masks must never silently inherit the first field's selection.
	for i, step := range steps {
		columns[i] = columns[proxyResolveOwner(steps, i)]
		columns[i].FieldID = step.FieldID
	}
	return columns, nil
}
`

const gatewayProxyParallel = `
func proxyResolveParallel(ctx context.Context, s *schema.Schema, steps []luvia.ExtendStep, keyCount int, params [4][]byte, clients map[string]*rpc.Client, columns []luvia.ExtendColumnResult) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var pending sync.WaitGroup
	resolve := func(i int) {
		column, err := proxyResolveField(ctx, s, steps[i], keyCount, params, clients, api.TraceDependencyParallel)
		if err != nil { cancel(err); return }
		columns[i] = column
	}
	// The caller executes one branch; only the other independent targets need goroutines.
	for i := 1; i < len(steps); i++ {
		if proxyResolveOwner(steps, i) != i { continue }
		pending.Go(func() { resolve(i) })
	}
	resolve(0)
	pending.Wait()
	return context.Cause(ctx)
}
`

const gatewayProxyField = `
func proxyResolveField(ctx context.Context, s *schema.Schema, ext luvia.ExtendStep, keyCount int, params [4][]byte, clients map[string]*rpc.Client, dependency api.TraceDependency) (luvia.ExtendColumnResult, error) {
	svcName := ext.SvcName
	response, err := clients[ext.Module].CallWithMaskTargetContext(ctx, luvia.BearerToken(ctx), s.APIs[svcName].ID, ext.Mask, params[ext.Shape()], rpc.TraceTarget{Operation: svcName, Field: ext.FieldName, Dependency: dependency})
	if err != nil { return luvia.ExtendColumnResult{}, fmt.Errorf("gateway: field %s via %s/%s: %w", ext.FieldName, ext.Module, svcName, err) }
	blobs := luvia.ParseGroupedResponse(response, ext.Nullable)
	if blobs == nil || len(blobs) != keyCount || !luvia.ValidateResolvedFields(blobs, ext, s) {
		return luvia.ExtendColumnResult{}, fmt.Errorf("gateway: invalid grouped response for field %s via %s/%s (expected %d keys)", ext.FieldName, ext.Module, svcName, keyCount)
	}
	return luvia.ExtendColumnResult{FieldID: ext.FieldID, Blobs: blobs}, nil
}
`

const gatewayProxyOwner = `
func proxyResolveOwner(steps []luvia.ExtendStep, index int) int {
	step := steps[index]
	for i, other := range steps[:index] {
		if step.Module == other.Module && step.SvcName == other.SvcName && step.Shape() == other.Shape() && bytes.Equal(step.Mask, other.Mask) { return i }
	}
	return index
}
`

const gatewayProxySingleTarget = `
func proxySingleTarget(steps []luvia.ExtendStep) bool {
	for i := 1; i < len(steps); i++ {
		if proxyResolveOwner(steps, i) != 0 { return false }
	}
	return true
}
`
