package template

import (
	"github.com/Masterminds/sprig"
)

// Use the sprig library's template functions.  While this is a break from previous versions of
// boilr, it has several advantages.
// - sprig is used by several large projects (such as helm) and therefore its functions are familiar
// - sprig's functions have the correct signatures for pipelining
// - sprig has, at the time of writing, over 100 functions
// The before-fork tag can be used to view the functions before this change. If we find they are
// needed, we should attempt PRs to sprig and only add them back here if they are not accepted.

var (
	FuncMap = sprig.TxtFuncMap()

	// Options contain the default options for the template execution.
	Options = []string{
		// TODO ignore a field if no value is found instead of writing <no value>
		"missingkey=invalid",
	}
)
