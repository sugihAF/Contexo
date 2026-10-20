package ui

import "github.com/sugihAF/contexo/internal/syncops/contract"

// ExitCode is the process exit code for err: 0 for nil, 1 in human mode
// (unchanged behaviour), and the contract exit code in envelope mode.
func ExitCode(err error, envelope bool) int {
	if err == nil {
		return contract.ExitOK
	}
	if !envelope {
		return contract.ExitInternal
	}
	return contract.ExitCodeFor(ToContractError(err).Code)
}
