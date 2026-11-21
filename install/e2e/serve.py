# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Serves a directory on 127.0.0.1 the way GitHub serves release files:
# every file as application/octet-stream, which is what `irm ... | iex`
# and `curl ... | sh` get from a release. install/e2e/test.sh and test.ps1
# run it.
#
#   python3 -I install/e2e/serve.py <port> <dir>
import functools
import http.server
import sys


class Handler(http.server.SimpleHTTPRequestHandler):
    def guess_type(self, path):
        return 'application/octet-stream'

    def log_message(self, format, *args):
        pass


def main():
    port, directory = int(sys.argv[1]), sys.argv[2]
    handler = functools.partial(Handler, directory=directory)
    with http.server.ThreadingHTTPServer(('127.0.0.1', port), handler) as server:
        server.serve_forever()


if __name__ == '__main__':
    main()
