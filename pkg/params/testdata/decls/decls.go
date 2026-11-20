// SPDX-License-Identifier: AGPL-3.0-or-later

// Package decls is a fixture for declaredParams in static_test.go. It
// declares parameter variables the way names.go does and in each wrong way
// TestDeclaredParams has to catch. The go command never builds testdata.
package decls

type (
	IntParam  struct{ intName string }
	RatParam  struct{ ratName string }
	BoolParam struct{ flagName string }
)

// The way names.go declares every parameter.
var (
	GOOD_S     = IntParam{"GOOD_S"}
	GOOD_RATIO = RatParam{"GOOD_RATIO"}
	GOOD_FLAG  = BoolParam{"GOOD_FLAG"}
)

// Other ways. TYPED_S and PAIR_A_S give their own names; the rest don't.
var (
	TYPED_S            IntParam = IntParam{"TYPED_S"}
	MISNAMED_S                  = IntParam{"OTHER_S"}
	KEYED_S                     = IntParam{intName: "KEYED_S"}
	EMPTY_S                     = IntParam{}
	BARE_S             IntParam
	PAIR_A_S, PAIR_B_S = IntParam{"PAIR_A_S"}, IntParam{"WRONG_S"}
)

// Not parameters.
var (
	list = []IntParam{GOOD_S}
	text = "NOT_A_PARAM"
)
