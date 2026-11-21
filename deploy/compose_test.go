// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import _ "embed"

// composeYAML is compose.yml, which the tests check against litestream.yml,
// the template and pairtimed's defaults. It is embedded in a test file so
// that pairtimed, which imports this package for its config template, isn't
// built from compose.yml: a pin bump builds no server image, and the restore
// drill reads it as a pin, not as a change to the image (server-image.yml's
// paths, tools/ci/restore_drill.sh's image_paths).
//
//go:embed compose.yml
var composeYAML string
