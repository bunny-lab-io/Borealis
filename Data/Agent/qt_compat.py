from __future__ import annotations

from typing import Any

QtCore = None
QtGui = None
QtWidgets = None
QEventLoop = None
QT_BINDING = ""

try:
    from PySide6 import QtCore as _QtCore, QtGui as _QtGui, QtWidgets as _QtWidgets

    QtCore = _QtCore
    QtGui = _QtGui
    QtWidgets = _QtWidgets
    QT_BINDING = "PySide6"
except Exception:
    try:
        from PyQt5 import QtCore as _QtCore, QtGui as _QtGui, QtWidgets as _QtWidgets

        QtCore = _QtCore
        QtGui = _QtGui
        QtWidgets = _QtWidgets
        QT_BINDING = "PyQt5"
    except Exception:
        QtCore = None
        QtGui = None
        QtWidgets = None
        QT_BINDING = ""

if QtCore is not None:
    try:
        from qasync import QEventLoop as _QEventLoop

        QEventLoop = _QEventLoop
    except Exception:
        QEventLoop = None


def qt_enum(namespace: Any, dotted_name: str, default: Any = None) -> Any:
    current = namespace
    for part in str(dotted_name or "").split("."):
        if not part:
            continue
        if current is None or not hasattr(current, part):
            return default
        current = getattr(current, part)
    return current if current is not None else default


def qt_exec(dialog: Any) -> Any:
    if hasattr(dialog, "exec"):
        return dialog.exec()
    if hasattr(dialog, "exec_"):
        return dialog.exec_()
    raise AttributeError("Qt dialog does not expose exec/exec_")
