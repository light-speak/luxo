package formatter

import (
	"os"
	"testing"
)

// Representative .luxo source for formatter benchmarks.
const benchFormatInput = `
enum Role {
  USER
  ADMIN
  MODERATOR
}

model User @crud {
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

api getLevel(score: Int): String {
  when(score) {
    in 90..100 -> "A"
    in 80..89 -> "B"
    in 60..79 -> "C"
    else -> "D"
  }
}
`

// BenchmarkFormat measures formatting of a medium-sized .luxo source.
func BenchmarkFormat(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Format(benchFormatInput, "bench.luxo")
	}
}

// BenchmarkFormatDemoFile measures formatting of the full demo.luxo file.
func BenchmarkFormatDemoFile(b *testing.B) {
	data, err := os.ReadFile("../../editors/vscode/examples/demo.luxo")
	if err != nil {
		b.Skipf("demo.luxo not found: %v", err)
	}
	src := string(data)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Format(src, "demo.luxo")
	}
}
