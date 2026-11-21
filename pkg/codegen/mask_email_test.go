package codegen

import "testing"

func TestGeneratedEmailMaskPreservesUnicode(t *testing.T) {
	runGeneratedFixture(t, `
model MailView @noTime {
 id: Int @id
 email: String @email @mask
 optional: String? @email @mask
 plain: String
}
api masked(email: String, optional: String? = null): MailView {
 return MailView { id: 1, email: email, optional: optional, plain: email }
}
api maskedList(email: String, optional: String? = null): [MailView] {
 return [MailView { id: 1, email: email, optional: optional, plain: email }]
}
`, generatedEmailMaskAssertions, StableIDs{
		APIs:        map[string]int{"masked": 1, "maskedList": 2},
		APIParams:   map[string]map[string]int{"masked": {"email": 1, "optional": 2}, "maskedList": {"email": 1, "optional": 2}},
		ModelFields: map[string]map[string]int{"MailView": {"id": 1, "email": 2, "optional": 3, "plain": 4}},
	})
}

const generatedEmailMaskAssertions = `package generated
import (
 "bytes"
 "encoding/json"
 "net"
 "net/http/httptest"
 "strconv"
 "testing"
 "unicode/utf8"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/light-speak/luxo/pkg/lux/rpc"
 "github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/light-speak/luxo/pkg/lux/selection"
)
type emailMaskResult struct { Email string; Optional *string; Plain string }
func TestEmailMaskRoutes(t *testing.T) {
 app:=&App{};router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{value,want string}{
  {"user@example.com","u***@example.com"},{"张三@example.com","张*@example.com"},
  {"张@example.com","张@example.com"},{"😀用户@例子.公司","😀**@例子.公司"},
 }{for _,list:=range []bool{false,true}{for _,present:=range []bool{false,true}{for mode:=range 3{
  name:="masked";if list{name="maskedList"}
  t.Run(name+"/"+sample.value+"/present="+strconv.FormatBool(present)+"/mode="+strconv.Itoa(mode),func(t *testing.T){
   data:=emailMaskCall(t,router,client,name,sample.value,present,mode)
   var result emailMaskResult
   if list{var rows []emailMaskResult;if err:=json.Unmarshal(data,&rows);err!=nil||len(rows)!=1{t.Fatal(string(data),err)};result=rows[0]}else if err:=json.Unmarshal(data,&result);err!=nil{t.Fatal(string(data),err)}
   if !utf8.Valid(data)||result.Email!=sample.want||result.Plain!=sample.value{t.Fatal(string(data),result,sample.want)}
   if present{if result.Optional==nil||*result.Optional!=sample.want{t.Fatal(result.Optional,sample.want)}}else if result.Optional!=nil{t.Fatal("null field became present",*result.Optional)}
  })
 }}}}
}
func emailMaskCall(t *testing.T,router *api.Router,client *rpc.Client,name,value string,present bool,mode int)[]byte{
 t.Helper();definition:=router.Schema.APIs[name]
 var parameters codec.Encoder
 if err:=parameters.WriteFieldString(1,value);err!=nil{t.Fatal(err)}
 if present{if err:=parameters.WriteFieldStringPtr(2,&value);err!=nil{t.Fatal(err)}}
 parameters.WriteEnd()
 fields:=[]*selection.Field{{Name:"id"},{Name:"email"},{Name:"optional"},{Name:"plain"}}
 mask,maskErr:=schema.SelectToFieldMask(fields,router.Schema.Models["MailView"],router.Schema);if maskErr!=nil{t.Fatal(maskErr)}
 var data []byte;var err error
 if mode==2{data,err=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,parameters.Bytes());if err!=nil{t.Fatal(err)}}else{
  optional:="null";if present{optional=strconv.Quote(value)}
  body:=[]byte("{\"$api\":"+strconv.Quote(name)+",\"$select\":\"id,email,optional,plain\",\"email\":"+strconv.Quote(value)+",\"optional\":"+optional+"}")
  if mode==1{body=codec.AppendVarint(codec.AppendVarint(nil,uint64(definition.ID)),uint64(len(mask)));body=append(body,mask...);body=append(body,parameters.Bytes()...)}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if mode==1{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(mode,response.Code,response.Body.String())}
  data=response.Body.Bytes()
  if mode==0{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(data,&envelope);err!=nil||len(envelope.Data)==0{t.Fatal(string(data),err)};return envelope.Data}
 }
 if name=="maskedList"{data,err=schema.BinaryListToJSON(nil,data,router.Schema.Models["MailView"],router.Schema)}else{data,err=schema.BinaryToJSON(nil,data,router.Schema.Models["MailView"],router.Schema)}
 if err!=nil{t.Fatal(err)};return data
}
`
