#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Python_API_Endpoints/ocr_engines.py

import os
import io
import sys
import base64
import torch
import pytesseract
import easyocr
import numpy as np
import platform
from PIL import Image

# ---------------------------------------------------------------------
# Configure cross-platform Tesseract path
# ---------------------------------------------------------------------
SYSTEM = platform.system()

def get_tesseract_folder():
    if getattr(sys, 'frozen', False):
        # PyInstaller EXE
        base_path = sys._MEIPASS
        return os.path.join(base_path, "Borealis", "Python_API_Endpoints", "Tesseract-OCR")
    else:
        # Normal Python environment
        base_dir = os.path.dirname(os.path.abspath(__file__))
        return os.path.join(base_dir, "Tesseract-OCR")

if SYSTEM == "Windows":
    TESSERACT_FOLDER = get_tesseract_folder()
    TESSERACT_EXE = os.path.join(TESSERACT_FOLDER, "tesseract.exe")
    TESSDATA_DIR = os.path.join(TESSERACT_FOLDER, "tessdata")

    if not os.path.isfile(TESSERACT_EXE):
        raise EnvironmentError(f"Missing tesseract.exe at expected path: {TESSERACT_EXE}")

    pytesseract.pytesseract.tesseract_cmd = TESSERACT_EXE
    os.environ["TESSDATA_PREFIX"] = TESSDATA_DIR
else:
    # Assume Linux/macOS with system-installed Tesseract
    pytesseract.pytesseract.tesseract_cmd = "tesseract"

# ---------------------------------------------------------------------
# EasyOCR Global Instances
# ---------------------------------------------------------------------
easyocr_reader_cpu = None
easyocr_reader_gpu = None

def initialize_ocr_engines():
    global easyocr_reader_cpu, easyocr_reader_gpu
    if easyocr_reader_cpu is None:
        easyocr_reader_cpu = easyocr.Reader(['en'], gpu=False)
    if easyocr_reader_gpu is None:
        easyocr_reader_gpu = easyocr.Reader(['en'], gpu=torch.cuda.is_available())

# ---------------------------------------------------------------------
# Main OCR Handler
# ---------------------------------------------------------------------
def run_ocr_on_base64(image_b64: str, engine: str = "tesseract", backend: str = "cpu") -> list[str]:
    if not image_b64:
        raise ValueError("No base64 image data provided.")

    try:
        raw_bytes = base64.b64decode(image_b64)
        image = Image.open(io.BytesIO(raw_bytes)).convert("RGB")
    except Exception as e:
        raise ValueError(f"Invalid base64 image input: {e}")

    engine = engine.lower().strip()
    backend = backend.lower().strip()

    if engine in ["tesseract", "tesseractocr"]:
        try:
            text = pytesseract.image_to_string(image, config="--psm 6 --oem 1")
        except pytesseract.TesseractNotFoundError:
            raise RuntimeError("Tesseract binary not found or not available on this platform.")
    elif engine == "easyocr":
        initialize_ocr_engines()
        reader = easyocr_reader_gpu if backend == "gpu" else easyocr_reader_cpu
        result = reader.readtext(np.array(image), detail=1)

        # Group by Y position (line-aware sorting)
        result = sorted(result, key=lambda r: r[0][0][1])
        lines = []
        current_line = []
        last_y = None
        line_threshold = 10

        for (bbox, text, _) in result:
            y = bbox[0][1]
            if last_y is None or abs(y - last_y) < line_threshold:
                current_line.append(text)
            else:
                lines.append(" ".join(current_line))
                current_line = [text]
            last_y = y

        if current_line:
            lines.append(" ".join(current_line))
        text = "\n".join(lines)
    else:
        raise ValueError(f"OCR engine '{engine}' not recognized.")

    return [line.strip() for line in text.splitlines() if line.strip()]
