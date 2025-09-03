# --- Stage 1: The Builder ---
FROM golang:1.24-alpine AS builder

# Install git, which is needed for 'go mod download'.
RUN apk --no-cache add git

# Set the working directory inside the container.
WORKDIR /app

# Copy the dependency files first to leverage Docker's build cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code.
COPY . .

# --- THIS IS THE FIX ---
# Build all plugins (adaptors) from their source code.
# The correct way to build plugins is to 'cd' into their directory first.
RUN mkdir -p /app/plugins
RUN (cd adaptors/cfplugin && go build -buildmode=plugin -o /app/plugins/cfplugin.so .)
RUN (cd adaptors/dns-adaptor && go build -buildmode=plugin -o /app/plugins/dns-adaptor.so .)
RUN (cd adaptors/hana-adaptor && go build -buildmode=plugin -o /app/plugins/hana-adaptor.so .)
# --- END OF FIX ---

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