{{- /* SPDX-License-Identifier: AGPL-3.0-or-later */ -}}
# Sign-ups aren't open yet

This pairtime server isn't taking new accounts right now.{{if not .Named}} Sign-ups open only after its [privacy notice](/privacy) names who is responsible for your data.{{end}}

No account was made for you, and nothing from your Hack Club sign-in was saved.

You can still use pairtime on your computer in local-only mode. It tracks your time and shows it to you there, and uploads nothing.

{{if .Named -}}
If you think you should be able to sign in, contact {{.Contact}}. The [privacy notice](/privacy) says what this server would keep about you.
{{- else -}}
The [privacy notice](/privacy) says what this server would keep about you once sign-ups open.
{{- end}}
