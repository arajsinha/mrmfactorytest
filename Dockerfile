# --- Stage 1: The Builder ---
# This stage uses a full Go development environment to compile everything.
FROM golang:1.23-alpine AS builder

# Install git, which is needed for 'go mod download' with some dependencies.
RUN apk --no-cache add git

# Set the working directory inside the container.
WORKDIR /app

# Copy the dependency files first to leverage Docker's build cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code.
COPY . .

# --- NEW: Build all plugins (adaptors) from their source code ---
# Create a directory to hold the compiled plugins.
RUN mkdir -p /app/plugins

# Compile each adaptor into a .so file and place it in the /app/plugins directory.
# Note: Ensure the source paths (e.g., ./adaptors/cfplugin) match your project structure.
RUN go build -buildmode=plugin -o /app/plugins/cfplugin.so ./adaptors/cfplugin/
RUN go build -buildmode=plugin -o /app/plugins/dns-adaptor.so ./adaptors/dns-adaptor/
RUN go build -buildmode=plugin -o /app/plugins/hana-adaptor.so ./adaptors/hana-adaptor/

# Build the main application into a single, static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/mrm-cell .


# --- Stage 2: The Final Image ---
# This stage creates the final, lightweight image that will be deployed.
FROM alpine:latest

WORKDIR /app

# Install CA certificates, needed for making HTTPS requests.
RUN apk --no-cache add ca-certificates

# Copy the compiled application binary from the 'builder' stage.
COPY --from=builder /app/mrm-cell .

# Copy the fsm-config.yaml file.
COPY fsm-config.yaml .

# --- NEW: Copy the entire directory of compiled plugins from the builder stage ---
COPY --from=builder /app/plugins/ ./plugins/

# Expose the ports that the application will listen on for documentation.
EXPOSE 9081 8082 8083 2379 2380 

# The command that will be run when a container is started from this image.
CMD ["./mrm-cell"]