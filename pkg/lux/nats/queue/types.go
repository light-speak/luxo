package queue

import corequeue "github.com/light-speak/luxo/pkg/lux/queue"

// Config and Handler preserve the portable queue contract without conversion.
type Config = corequeue.Config
type Handler = corequeue.Handler
type Queue = corequeue.Queue

const dlqSuffix = corequeue.DeadLetterSuffix
