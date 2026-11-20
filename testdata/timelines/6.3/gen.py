# SPDX-License-Identifier: AGPL-3.0-or-later
"""SM 6.3, the five-project afternoon. See README.md in this directory;
afternoon.py builds the timeline, shared with 6.3-tabs."""

import os
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
sys.path.insert(0, HERE)

import afternoon  # noqa: E402
import timeline as tl  # noqa: E402

fx, expected, anchors, prompts = afternoon.build(tabs=False)
fx.write(tl.out_dir(HERE), expected, anchors, prompts)
