from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))

from vylux_manual.cli import main


if __name__ == "__main__":
    main()
