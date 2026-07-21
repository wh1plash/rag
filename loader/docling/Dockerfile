# Используем официальный Python образ
FROM python:3.11-slim

# Необходимые системные зависимости
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      libgl1 \
      libglib2.0-0 \
      tesseract-ocr \
      libtesseract-dev \
      poppler-utils \
      libjpeg-dev \
      libpng-dev \
      zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

# Создаём рабочую директорию
WORKDIR /app

# EasyOCR pulls remote model zips at runtime and often hangs/fails behind firewalls.
# Use system tesseract (apt above) via pytesseract — skip the heavy [tesserocr] extra.
RUN pip install --upgrade pip && \
    pip install --no-cache-dir --default-timeout=300 --retries 10 \
      docling-serve \
      pytesseract

# Экспорт порта (по умолчанию docling-serve слушает 5001)
EXPOSE 5001

# Переменные окружения (по желанию)
ENV DOCLING_SERVE_ENABLE_UI=0
ENV ENVIRONMENT=production
ENV DOCLING_OCR_ENABLED=true
ENV DOCLING_SERVE_DEFAULT_OCR_KIND=tesseract
ENV DOCLING_SERVE_DEFAULT_OCR_PRESET=tesseract

# Команда запуска сервиса
CMD ["docling-serve", "run", "--host", "0.0.0.0", "--port", "5001"]
