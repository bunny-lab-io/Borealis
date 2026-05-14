from __future__ import annotations

from typing import Any, Dict, List, Tuple


TRAY_POPUP_MARGIN = 0


def popup_palette(tone: str) -> Dict[str, str]:
    normalized = str(tone or "healthy").strip().lower() or "healthy"
    palettes = {
        "healthy": {
            "accent": "#38d39f",
            "accent_soft": "rgba(56, 211, 159, 0.16)",
            "accent_border": "rgba(56, 211, 159, 0.42)",
        },
        "neutral": {
            "accent": "#69b7ff",
            "accent_soft": "rgba(105, 183, 255, 0.16)",
            "accent_border": "rgba(105, 183, 255, 0.42)",
        },
        "warning": {
            "accent": "#f0b34c",
            "accent_soft": "rgba(240, 179, 76, 0.16)",
            "accent_border": "rgba(240, 179, 76, 0.42)",
        },
        "error": {
            "accent": "#f06f6f",
            "accent_soft": "rgba(240, 111, 111, 0.16)",
            "accent_border": "rgba(240, 111, 111, 0.42)",
        },
    }
    return palettes.get(normalized, palettes["healthy"])


def bottom_right_anchor(
    left: int,
    top: int,
    width: int,
    height: int,
    popup_width: int,
    popup_height: int,
    *,
    margin: int = TRAY_POPUP_MARGIN,
) -> Tuple[int, int]:
    max_x = int(left) + int(width) - int(popup_width)
    max_y = int(top) + int(height) - int(popup_height)
    x = max(int(left), min(max_x - int(margin), max_x))
    y = max(int(top), min(max_y - int(margin), max_y))
    return x, y


def warning_lines(view: Dict[str, Any]) -> List[str]:
    return [str(item).strip() for item in (view.get("warnings") or []) if str(item).strip()]
