package codegen

import (
	"fmt"
	"strings"
)

// Aliases preserve the owner's static payload identity and bounded codecs.
func (g *GeneratorContext) writeSealedAlias(b *strings.Builder, name, prefix string) {
	public := name
	if g.valueTypes.publicTypes[name] != "" {
		public = g.valueTypes.publicTypes[name]
	}
	fmt.Fprintf(b, "type %s = %s%s\n", name, prefix, public)
	fmt.Fprintf(b, "func ReadJSONInput%sList(raw []byte)([]%s,error){return %sReadJSONInput%sList(raw)}\n", name, name, prefix, name)
	fmt.Fprintf(b, "func ReadColumnar%s(raw []byte)([]%s,error){return %sReadColumnar%s(raw)}\n", name, name, prefix, name)
	reader := g.depthColumnarFunction("ReadColumnar" + name)
	fmt.Fprintf(b, "func %s(raw []byte,depth int)([]%s,error){return %s%s(raw,depth)}\n", reader, name, prefix, reader)
	writer := "WriteColumnar" + name
	fmt.Fprintf(b, "func %s(buf *api.ResponseBuf,items []%s,mask []byte)error{return %s%s(buf,items,mask)}\n", writer, name, prefix, writer)
	depthWriter := g.depthColumnarFunction(writer)
	fmt.Fprintf(b, "func %s(buf *api.ResponseBuf,items []%s,mask []byte,depth int)error{return %s%s(buf,items,mask,depth)}\n", depthWriter, name, prefix, depthWriter)
}
