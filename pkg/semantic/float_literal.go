package semantic

import (
	"math"
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
)

func (a *Analyzer) validateFloatLiteral(literal *ast.Literal) {
	value, err := strconv.ParseFloat(literal.Value, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		a.addError(literal.Pos, "Float literal must represent a finite Float64 / Float 字面量必须能表示为有限 Float64")
	}
}
