package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedNativeEnumStream(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "native.luxo", Enums: []*ast.EnumDecl{{Name: "State", Values: []string{"OPEN", "CLOSED"}}}, APIs: []*ast.ApiDecl{
		{Name: "watch", ReturnType: &ast.TypeRef{Name: "State"}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}},
	}}}}
	var assertions strings.Builder
	assertions.WriteString(nativeEnumStreamAssertions)
	generateEnum(&assertions, result.Files[0].Enums[0])
	runGeneratedProtocolPackage(t, string(generateStreamFile(result, "generated")), assertions.String())
}

func TestEnumAppendDetectionIgnoresStringLiterals(t *testing.T) {
	var source strings.Builder
	source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux/codec\")\nfunc encodedLength() []byte {var dst []byte\n")
	writeBinaryAppend(&source, "\t", "dst", `codec.AppendSvarint(dst, int64(len(").AppendLuxo(")))`, "return nil")
	source.WriteString("return dst\n}\n")
	runGeneratedProtocolPackage(t, source.String(), `package generated
import ("testing";"github.com/light-speak/luxo/pkg/lux/codec")
func TestLiteral(t *testing.T) {dec:=codec.NewDecoder(encodedLength());if got:=dec.ReadInt();got!=13 || dec.Finish()!=nil {t.Fatalf("literal length: %d %v",got,dec.Err())}}
`)
}

const nativeEnumStreamAssertions = `package generated
import (
    "context"
    "errors"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
type nativeEnumResolver struct{ value State }
func (r nativeEnumResolver) HandleWatch(_ context.Context,_ *WatchStreamParams,_ any,stream *api.TypedStream[State]) error {return stream.Send(r.value)}
func TestNativeEnumStream(t *testing.T) {
    for _,value:=range []State{StateOPEN,"FUTURE"} {
        router:=api.NewRouter()
        if err:=RegisterStreams(router,nil,nativeEnumResolver{value});err!=nil{t.Fatal(err)}
        sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:"watch",BinaryMode:true})
        if err!=nil {t.Fatal(err)}
        count:=0
        for data:=range sub.Messages() {
            dec:=codec.NewDecoder(data)
            if dec.ReadString()!=string(value) || dec.Err()!=nil {t.Fatalf("bad stream payload: %x",data)}
            count++
        }
        sub.Close()
        if value==StateOPEN {if count!=1 || sub.Err()!=nil {t.Fatalf("known: %d %v",count,sub.Err())}} else if count!=0 || !errors.Is(sub.Err(),codec.ErrInvalidEnum) {t.Fatalf("unknown: %d %v",count,sub.Err())}
    }
}
`

func TestGeneratedEnumOutput(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			var source strings.Builder
			source.WriteString(scalarWriterSource(declaration, "State", "State", map[string]bool{"State": true}))
			for _, list := range []bool{false, true} {
				goType := "State"
				if list {
					goType = "[]State"
				}
				fmt.Fprintf(&source, "func encodeStream%t(result %s) ([]byte,error) { buf:=api.GetBuf()\n", list, goType)
				writeStreamPayloadEncoding(&source, streamInfo{returnType: &ast.TypeRef{Name: "State", IsList: list}, payloadEnum: true}, "result", "\t")
				source.WriteString("data:=append([]byte(nil),buf.B...); api.PutBuf(buf); return data,nil\n}\n")
			}
			runGeneratedProtocolPackage(t, source.String(), enumOutputAssertions)
		})
	}
}

const enumOutputAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

    "bytes"
    "context"
    "errors"
    "net/http"
    "net/http/httptest"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)

func enumMask(t testing.TB, ids ...int) []byte {
    var fields []byte
    for _,id:=range ids {fields=testutil.Bytes(t)(codec.FieldMaskSet(fields,id))}
    return testutil.Bytes(t)(codec.AppendSelectionMask(nil,fields,nil))
}

func TestRejectSelectedUnknownEnums(t *testing.T) {
    for _,value:=range []State{"FUTURE","","open"} {
        items:=[]Payload{
            {Value:value}, {Value:StateOPEN,Optional:&value},
            {Value:StateOPEN,Values:[]State{StateOPEN,value}},
            {Value:StateOPEN,OptionalValues:[]State{StateOPEN,value}},
            {Value:StateOPEN,Child:&Child{Value:value}},
            {Value:StateOPEN,Children:[]Child{{Value:StateOPEN},{Value:value}}},
        }
        for i,item:=range items {
            for _,column:=range []bool{false,true} {
                write:=func(mask []byte)error {
                    buf:=&api.ResponseBuf{}
                    if column {return WriteColumnarPayload(buf,[]Payload{item},mask)}
                    return item.WriteLuxo(buf,mask)
                }
                if err:=write(enumMask(t, i+1)); !errors.Is(err,codec.ErrInvalidEnum) {t.Fatalf("field=%d column=%t: %v",i+1,column,err)}
                skip:=1; if i==0 {skip=2}
                if err:=write(enumMask(t, skip)); err!=nil {t.Fatalf("unselected enum inspected: %v",err)}
            }
        }
        for _,encode:=range []func(*api.Request,State)error{encodeNative,encodeCompiled,encodeLiteral} {
            req:=&api.Request{Buf:&api.ResponseBuf{},FieldMask:enumMask(t, 1)}
            if err:=encode(req,value); !errors.Is(err,codec.ErrInvalidEnum) {t.Fatal(err)}
        }
        for _,encode:=range []func(*api.Request,[]State)error{encodeNativeList,encodeCompiledList} {
            if err:=encode(&api.Request{Buf:&api.ResponseBuf{}},[]State{StateOPEN,value}); !errors.Is(err,codec.ErrInvalidEnum) {t.Fatal(err)}
        }
        if data,err:=encodeStreamfalse(value); !errors.Is(err,codec.ErrInvalidEnum) || data!=nil {t.Fatalf("stream: %x %v",data,err)}
        if data,err:=encodeStreamtrue([]State{StateOPEN,value}); !errors.Is(err,codec.ErrInvalidEnum) || data!=nil {t.Fatalf("stream list: %x %v",data,err)}
    }
}

func TestEnumOutputPreservesWire(t *testing.T) {
    for _,value:=range []State{StateOPEN,StateCLOSED} {
        want,err:=codec.AppendString(nil,string(value)); if err!=nil {t.Fatal(err)}
        for _,encode:=range []func(*api.Request,State)error{encodeNative,encodeCompiled} {
            req:=&api.Request{Buf:&api.ResponseBuf{}}
            if err:=encode(req,value); err!=nil || !bytes.Equal(req.Buf.B,want) {t.Fatalf("wire: %x %v",req.Buf.B,err)}
        }
        if data,err:=encodeStreamfalse(value); err!=nil || !bytes.Equal(data,want) {t.Fatalf("stream wire: %x %v",data,err)}
        for _,encode:=range []func(*api.Request,[]State)error{encodeNativeList,encodeCompiledList} {
            req:=&api.Request{Buf:&api.ResponseBuf{}}
            if err:=encode(req,[]State{value}); err!=nil || !bytes.Equal(req.Buf.B,append([]byte{1},want...)) {t.Fatalf("list wire: %x %v",req.Buf.B,err)}
        }
    }
}

func TestEnumOutputFailureDoesNotPublishSuccess(t *testing.T) {
    for _,binary:=range []bool{false,true} {
        for _,encode:=range []func(*api.Request,State)error{encodeNative,encodeCompiled,encodeLiteral} {
            router:=api.NewRouter()
            router.Registry.Register("invalidEnum",1)
            var encodingError error
            router.Handle("invalidEnum",func(_ context.Context,req *api.Request)error {req.FieldMask=enumMask(t, 1);encodingError=encode(req,State("FUTURE"));return encodingError})
            body:=[]byte("{\"$api\":\"invalidEnum\"}")
            if binary {body=[]byte{1,0,0}}
            request:=httptest.NewRequest(http.MethodPost,"/",bytes.NewReader(body))
            if binary {request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")} else {request.Header.Set("Content-Type","application/json")}
            response:=httptest.NewRecorder()
            router.ServeHTTP(response,request)
            if !errors.Is(encodingError,codec.ErrInvalidEnum) {t.Fatalf("handler did not reject enum: %v",encodingError)}
            if response.Code!=http.StatusInternalServerError || bytes.Contains(response.Body.Bytes(),[]byte("FUTURE")) {t.Fatalf("failed output escaped: binary=%t status=%d body=%s",binary,response.Code,response.Body.String())}
        }
    }
}

func TestEnumAppendIsAtomic(t *testing.T) {
    original:=[]byte("prefix")
    got,err:=State("FUTURE").AppendLuxo(original)
    if !errors.Is(err,codec.ErrInvalidEnum) || !bytes.Equal(got,original) || &got[0]!=&original[0] {t.Fatalf("failed append changed destination: %x %v",got,err)}
}
`
