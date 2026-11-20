# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.3's two-tab variant: A and B in two tabs of one iTerm window. See
README.md in this directory; ../6.3/afternoon.py builds the timeline."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "6.3"))

import afternoon  # noqa: E402
import timeline as tl  # noqa: E402

fx, expected, anchors, prompts = afternoon.build(tabs=True)
fx.write(tl.out_dir(HERE), expected, anchors, prompts)
