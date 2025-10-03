"""Borealis controller shims executed on interpreter startup."""
from __future__ import annotations

import logging
import multiprocessing

log = logging.getLogger(__name__)

try:
    multiprocessing.set_start_method('spawn')
except RuntimeError:
    pass

try:
    mp_ctx = multiprocessing.get_context('fork')
except ValueError:
    try:
        import ansible.utils.multiprocessing as ansible_mp  # type: ignore
    except Exception:
        log.debug('Borealis sitecustomize: ansible multiprocessing module missing; skipping fork patch')
    else:
        ansible_mp.context = multiprocessing.get_context('spawn')
        log.debug('Borealis sitecustomize: patched ansible multiprocessing context to spawn')
else:
    log.debug('Borealis sitecustomize: fork context available (%s)', mp_ctx)
