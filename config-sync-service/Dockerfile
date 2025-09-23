# --- Stage 1: The Builder ---
# This stage compiles our Go application into a single binary.
FROM golang:1.24-alpine AS builder

# Install git, which is needed for 'go mod download'.
RUN apk --no-cache add git

WORKDIR /app

# Copy dependency files and download them.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code.
COPY . .

# Build the application into a static binary.
RUN CGO_ENABLED=0 go build -o /app/config-sync .


# --- Stage 2: The Final Image ---
# This stage creates the final, lightweight image that will be deployed.
FROM alpine:latest

WORKDIR /app

# --- THIS IS THE CRITICAL PART ---
# This container needs both git (to clone) and kubectl (to apply ConfigMaps)
# to perform its function at runtime.
RUN apk --no-cache add git kubectl

# Copy the compiled application binary from the 'builder' stage.
COPY --from=builder /app/config-sync .

# The command that will be run when the container starts.
CMD ["./config-sync"]
