# --- Stage 1: The Builder ---
# This stage compiles our Go application into a single binary.
FROM golang:1.24-alpine AS builder

WORKDIR /app

# The build context will be the repository root.
# Copy the dependency files from the root first.
COPY go.mod go.sum ./
RUN go mod download

# Now, copy only the source code for this specific service.
COPY mcp-server/ ./mcp-server/

# Build the application from within its subdirectory.
RUN CGO_ENABLED=0 go build -o /app/mcp-server ./mcp-server/


# --- Stage 2: The Final Image ---
# This stage creates the final, lightweight image.
FROM alpine:latest

WORKDIR /app

# The mcp-server does not need any extra tools to run.

# Copy the compiled application binary from the 'builder' stage.
COPY --from=builder /app/mcp-server .

# The command that will be run when the container starts.
CMD ["./mcp-server"]