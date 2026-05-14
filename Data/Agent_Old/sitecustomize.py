"""Borealis controller shims executed on interpreter startup."""
from __future__ import annotations

import logging
import multiprocessing

log = logging.getLogger(__name__)

try:
    multiprocessing.set_start_method('spawn')
except RuntimeError:
    pass
log.debug('Borealis sitecustomize: multiprocessing start method set to spawn when available')
