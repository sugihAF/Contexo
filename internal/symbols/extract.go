package symbols

import (
	"fmt"
	"strings"
)

// EncodeSymbolKey creates a symbol_key from file path and symbol name.
// Format: "file::symbol" (e.g. "auth/handler.go::HandleLogin")
func EncodeSymbolKey(file, symbol string) string {
	return fmt.Sprintf("%s::%s", file, symbol)
}

// DecodeSymbolKey splits a symbol_key into file and symbol parts.
func DecodeSymbolKey(key string) (file, symbol string) {
	parts := strings.SplitN(key, "::", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return key, ""
}

// ParseBlameArg parses a "file#symbol" argument into file and symbol.
func ParseBlameArg(arg string) (file, symbol string) {
	parts := strings.SplitN(arg, "#", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return arg, ""
}
