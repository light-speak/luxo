package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGenerateEnum(t *testing.T) {
	tests := []struct {
		name   string
		enum   *ast.EnumDecl
		expect string
	}{
		{
			name: "basic enum",
			enum: &ast.EnumDecl{
				Name:   "Role",
				Values: []string{"USER", "ADMIN", "MODERATOR"},
			},
			expect: `type Role string

const (
	RoleUSER      Role = "USER"
	RoleADMIN     Role = "ADMIN"
	RoleMODERATOR Role = "MODERATOR"
)
`,
		},
		{
			name: "single value enum",
			enum: &ast.EnumDecl{
				Name:   "Status",
				Values: []string{"ACTIVE"},
			},
			expect: `type Status string

const (
	StatusACTIVE Status = "ACTIVE"
)
`,
		},
		{
			name: "order status enum",
			enum: &ast.EnumDecl{
				Name:   "OrderStatus",
				Values: []string{"PENDING", "PAID", "SHIPPED", "COMPLETED", "CANCELLED"},
			},
			expect: `type OrderStatus string

const (
	OrderStatusPENDING   OrderStatus = "PENDING"
	OrderStatusPAID      OrderStatus = "PAID"
	OrderStatusSHIPPED   OrderStatus = "SHIPPED"
	OrderStatusCOMPLETED OrderStatus = "COMPLETED"
	OrderStatusCANCELLED OrderStatus = "CANCELLED"
)
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			generateEnum(&b, tt.enum)
			got := b.String()
			if got != tt.expect {
				t.Errorf("generateEnum mismatch:\ngot:\n%s\nexpect:\n%s", got, tt.expect)
			}
		})
	}
}
