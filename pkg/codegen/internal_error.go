package codegen

import "runtime/debug"

// InternalCompilerError identifies a failed code-generator invariant, rather
// than a user syntax error. No partial compilation is returned with this error.
type InternalCompilerError struct {
	Detail string
	Stack  []byte
}

func (e *InternalCompilerError) Error() string { return "internal compiler error: " + e.Detail }

// recoverCompilerFailure catches only our explicit invariant failures. Runtime
// panics and unrelated defects are not silently reclassified or swallowed.
func recoverCompilerFailure(result **GenerateResult, err *error) {
	if value := recover(); value != nil {
		failure, ok := value.(*InternalCompilerError)
		if !ok {
			panic(value)
		}
		failure.Stack = debug.Stack()
		*result = nil
		*err = failure
	}
}
