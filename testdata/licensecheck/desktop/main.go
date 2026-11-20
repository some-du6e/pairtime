// SPDX-License-Identifier: AGPL-3.0-or-later

// Command desktop imports only the standard library. Its one other import,
// example.com/licensecheck/gpl, is in a desktop test, for
// tools/ci/licensecheck to find.
package main

import "fmt"

func main() { fmt.Println("desktop") }
