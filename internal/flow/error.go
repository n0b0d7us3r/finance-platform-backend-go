package flow

import (
	"fmt"
	"sort"
)

// Error is a client-visible failure. Plain is a body with only message.
// Fields is a validation 422 body. Retry means the caller may wait and
// try the same assignment again: no order row has been written.
type Error struct {
	Status  int
	Message string
	Plain   bool
	Retry   bool
	Fields  map[string][]string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("flow status %d", e.Status)
}

func fail(status int, message string) *Error {
	return &Error{Status: status, Message: message}
}

func requisiteMiss() *Error {
	return &Error{Status: 400, Message: "Подходящие платежные реквизиты не найдены.", Retry: true}
}

func plain(status int, message string) *Error {
	return &Error{Status: status, Message: message, Plain: true}
}

func invalid(fields map[string][]string) *Error {
	return &Error{Status: 422, Message: validationMessage(fields), Fields: fields}
}

// Invalid is a 422 whose message names the first problem and how many remain.
func Invalid(fields map[string][]string) *Error {
	return invalid(fields)
}

func validationMessage(fields map[string][]string) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	first := ""
	extra := 0
	for _, key := range keys {
		for _, message := range fields[key] {
			if first == "" {
				first = message
				continue
			}
			extra++
		}
	}
	if extra == 0 {
		return first
	}
	return fmt.Sprintf("%s (and %d more errors)", first, extra)
}
