package lexer

import (
	"os"
	"testing"
)

// Representative .luxo source covering common token types.
const benchInput = `
use http
use json

val APP_NAME = "MyShop"
val MAX_RETRIES = 3

enum Role {
  USER
  ADMIN
  MODERATOR
}

model User @withAuth(stores: [id, role], default: true) @crud {
  id:        Int
  name:      String   @filterable
  email:     String   @unique
  password:  String   @hidden @hash
  role:      Role     = Role.USER
  avatar:    String?
  phone:     String   @immutable
  createdAt: DateTime
}

type AuthResult {
  token: String
  user:  User
}

error NotFound(resource: String) {
  code:    404
  message: "error.not_found"
}

event PostCreated(post: Post, userId: Int)

api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw PasswordTooShort
  val exists = User.where(email == input.email).first()
  exists == null ?: throw DuplicateEmail
  val user = User.create(
    name: input.name,
    email: input.email,
    password: input.password,
    phone: input.phone
  )
  AuthResult { token: user.createToken(), user: user }
}

api createOrder(productId: Int, quantity: Int): Order @auth {
  val product = Product.find(id: productId) ?: throw NotFound
  product.stock >= quantity ?: throw OutOfStock
  val total = product.price * quantity
  val finalPrice = when {
    my.role == Role.ADMIN -> total * 0.5
    my.role == Role.MODERATOR -> total * 0.8
    total > 1000 -> total * 0.95
    else -> total
  }
  val order = transaction {
    product.stock -= quantity
    product.update(stock: product.stock)
    Order.create(
      orderNo: crypto.randomToken(16),
      userId: my.id,
      productId: productId,
      quantity: quantity,
      total: finalPrice,
      status: OrderStatus.PENDING
    )
  }
  order
}
`

const benchTemplateInput = `
api formatUser(user: User): String {
  val name = user.name
  val email = user.email
  val result = "User: ${name}, Email: ${email}, Role: ${user.role}"
  val nested = "Order: ${order.id} for ${user.name} (total: ${order.total})"
  val multiline = "Stats: count=${stats.count}, avg=${stats.avg}, max=${stats.max}"
  result
}
`

// BenchmarkTokenize measures tokenization of a medium-sized .luxo source.
func BenchmarkTokenize(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		l := New(benchInput, "bench.luxo")
		l.Tokenize()
	}
}

// BenchmarkTokenizeStringTemplate measures tokenization of string templates with ${expr}.
func BenchmarkTokenizeStringTemplate(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		l := New(benchTemplateInput, "bench.luxo")
		l.Tokenize()
	}
}

// BenchmarkTokenizeDemoFile measures tokenization of the full demo.luxo file.
func BenchmarkTokenizeDemoFile(b *testing.B) {
	data, err := os.ReadFile("../../editors/vscode/examples/demo.luxo")
	if err != nil {
		b.Skipf("demo.luxo not found: %v", err)
	}
	src := string(data)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		l := New(src, "demo.luxo")
		l.Tokenize()
	}
}
