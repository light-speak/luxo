module example.com/luxo-consumer

go 1.27.1

require github.com/light-speak/luxo v0.0.0

require github.com/light-speak/luxo/pkg/lux/pg v1.0.0-beta.1

require (
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

replace github.com/light-speak/luxo => ../..

replace github.com/light-speak/luxo/pkg/lux/pg => ../../pkg/lux/pg
