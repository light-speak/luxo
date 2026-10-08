package pg

import pgdialect "github.com/light-speak/luxo/pkg/lux/dialect/pg"

// Dialect shares the compiler's driver-free SQL dialect without a wrapper call.
type Dialect = pgdialect.Dialect
