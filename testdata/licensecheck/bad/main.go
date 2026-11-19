// SPDX-License-Identifier: AGPL-3.0-or-later

// Command bad imports example.com/licensecheck/gpl, for tools/ci/licensecheck to judge.
package main

import (
	"fmt"

	"example.com/licensecheck/gpl"
)

func main() { fmt.Println(gpl.Name()) }
