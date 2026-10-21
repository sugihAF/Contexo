package syncops

import "github.com/sugihAF/contexo/internal/syncops/contract"

// NotImplemented is the error of a wave-0 stub (E17): code not_implemented,
// exit 10. The extension treats it like a too-old ctx.
func NotImplemented(op string) *contract.Error {
	return contract.New(contract.CodeNotImplemented,
		op+": not implemented in this ctx build", "ctx update")
}

// New builds a contract error for an engine failure.
func New(code contract.ErrorCode, msg string, advice ...string) *contract.Error {
	return contract.New(code, msg, advice...)
}

// Wrap builds a contract error around err. The message is msg + ": " +
// err.Error(), or err.Error() when msg is empty; errors.Is/As still reach err.
func Wrap(err error, code contract.ErrorCode, msg string, advice ...string) *contract.Error {
	m := msg
	if err != nil {
		if m == "" {
			m = err.Error()
		} else {
			m = msg + ": " + err.Error()
		}
	}
	e := contract.New(code, m, advice...)
	e.Err = err
	return e
}
