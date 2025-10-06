# --- Stage 1: The Builder ---
FROM golang:1.24-alpine AS builder

RUN apk --no-cache add git

WORKDIR /app

# The build context is the repository root.
# This command correctly copies the go.mod and go.sum from the root.
COPY go.mod go.sum ./
RUN go mod download

# Now, copy ONLY the source code for this specific service into the container.
COPY config-sync-service/ ./config-sync-service/

# --- THIS IS THE FIX ---
# Build the application from within its own subdirectory.
RUN (cd config-sync-service && CGO_ENABLED=0 go build -o /app/config-sync .)
# --- END OF FIX ---


# --- Stage 2: The Final Image ---
FROM alpine:latest

WORKDIR /app

# This container needs git and kubectl to do its job at runtime.
RUN apk --no-cache add git kubectl

# Copy the compiled application binary from the 'builder' stage.
COPY --from=builder /app/config-sync .

# The command to run when the container starts.
CMD ["./config-sync"]