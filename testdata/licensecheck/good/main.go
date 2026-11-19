// SPDX-License-Identifier: AGPL-3.0-or-later

// Command good imports example.com/licensecheck/mit, for tools/ci/licensecheck to judge.
package main

import (
	"fmt"

	"example.com/licensecheck/mit"
)

func main() { fmt.Println(mit.Name()) }
