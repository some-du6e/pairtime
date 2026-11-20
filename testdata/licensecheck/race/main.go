// SPDX-License-Identifier: AGPL-3.0-or-later

// Command race imports only the standard library. Its one other import,
// example.com/licensecheck/gpl, is in a test that builds only with -race,
// for tools/ci/licensecheck to find.
package main

import "fmt"

func main() { fmt.Println("race") }
