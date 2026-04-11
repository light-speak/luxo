package selection

import "testing"

func BenchmarkParseSimple(b *testing.B) {
	for range b.N {
		Parse("name,email,age")
	}
}

func BenchmarkParseNested(b *testing.B) {
	for range b.N {
		Parse("name,email,posts{title,content,comments{content,user{name,avatar}}}")
	}
}

func BenchmarkParseWide(b *testing.B) {
	input := "a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q,r,s,t,u,v,w,x,y,z"
	for range b.N {
		Parse(input)
	}
}
