# --- Stage 1: The Builder ---
FROM golang:1.24-alpine AS builder

# --- THIS IS THE FIX ---
# Install git (for go mod) AND build-base (for the C toolchain needed by plugins).
# The 'build-base' package includes gcc, make, and other tools required for cgo.
RUN apk --no-cache add git build-base
# --- END OF FIX ---

# Set the working directory inside the container.
WORKDIR /app

# Copy the dependency files first to leverage Docker's build cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code.
COPY . .

# Build all plugins (adaptors) from their source code.
RUN mkdir -p /app/plugins
RUN (cd adaptors/cfplugin && go build -buildmode=plugin -o /app/plugins/cfplugin.so .)
RUN (cd adaptors/dns-adaptor-route53 && go build -buildmode=plugin -o /app/plugins/dns-adaptor-route53.so .)
RUN (cd adaptors/hana-adaptor && go build -buildmode=plugin -o /app/plugins/hana-adaptor.so .)

# Build the main application into a single, static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/mrm-cell .


# --- Stage 2: The Final Image ---
FROM alpine:latest

WORKDIR /app

# Install CA certificates, needed for making HTTPS requests.
RUN apk --no-cache add ca-certificates

# Copy the compiled application binary from the 'builder' stage.
COPY --from=builder /app/mrm-cell .

# Copy the fsm-config.yaml file.
COPY fsm-config.yaml .

# Copy the entire directory of compiled plugins from the builder stage.
COPY --from=builder /app/plugins/ ./plugins/

# Expose the ports that the application will listen on for documentation.
EXPOSE 9081 8082 8083 2379 2380 

# The command that will be run when a container is started from this image.
CMD ["./mrm-cell"]