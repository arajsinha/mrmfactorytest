FROM python:3.11-slim
WORKDIR /app
# --- THIS IS THE FINAL FIX ---
# Install curl, which is needed to shut down the Istio sidecar.
RUN apt-get update && apt-get install -y curl
# --- END OF FIX ---
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY crew.py .
ENTRYPOINT ["python", "crew.py"]