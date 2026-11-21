package codegen

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/token"
)

func columnDefault(field *ast.FieldDecl) (lux.ColumnDefault, error) {
	switch value := field.Default.(type) {
	case *ast.Literal:
		if value.Kind == token.Null {
			if field.Type.Nullable {
				return lux.ColumnDefault{Kind: lux.DefaultNull}, nil
			}
			if field.Type.Name == "JSON" && !field.Type.IsList {
				return lux.ColumnDefault{Kind: lux.DefaultJSONNull}, nil
			}
			return lux.ColumnDefault{}, fmt.Errorf("null default requires a nullable field or JSON")
		}
		return literalColumnDefault(value, "")
	case *ast.UnaryExpr:
		literal, ok := value.Value.(*ast.Literal)
		if ok && (value.Op == "+" || value.Op == "-") {
			return literalColumnDefault(literal, value.Op)
		}
	case *ast.Ident:
		return lux.ColumnDefault{Kind: lux.DefaultString, Text: value.Name}, nil
	case *ast.MemberExpr:
		return lux.ColumnDefault{Kind: lux.DefaultString, Text: value.Field}, nil
	}
	return lux.ColumnDefault{}, fmt.Errorf("unsupported column default %T", field.Default)
}

func literalColumnDefault(literal *ast.Literal, sign string) (lux.ColumnDefault, error) {
	if sign == "" {
		switch literal.Kind {
		case token.String:
			return lux.ColumnDefault{Kind: lux.DefaultString, Text: literal.Value}, nil
		case token.True, token.False:
			return lux.ColumnDefault{Kind: lux.DefaultBoolean, Bool: literal.Kind == token.True}, nil
		}
	}
	value := sign + literal.Value
	switch literal.Kind {
	case token.Int:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return lux.ColumnDefault{Kind: lux.DefaultInt, Int: parsed}, err
	case token.Float:
		parsed, err := strconv.ParseFloat(value, 64)
		return lux.ColumnDefault{Kind: lux.DefaultFloat, Float: parsed}, err
	case token.Duration:
		parsed, err := migrationDuration(value)
		return lux.ColumnDefault{Kind: lux.DefaultDuration, Int: int64(parsed)}, err
	}
	return lux.ColumnDefault{}, fmt.Errorf("unsupported literal default %s", literal.Kind)
}

func migrationDuration(value string) (time.Duration, error) {
	if !strings.HasSuffix(value, "d") {
		return time.ParseDuration(value)
	}
	days, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 64)
	if err != nil {
		return 0, err
	}
	const unit = int64(24 * time.Hour)
	if days < math.MinInt64/unit || days > math.MaxInt64/unit {
		return 0, fmt.Errorf("Duration day default exceeds Int64 nanoseconds")
	}
	return time.Duration(days * unit), nil
}
