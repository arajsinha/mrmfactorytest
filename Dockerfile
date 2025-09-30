# --- Stage 1: The Builder ---
FROM golang:1.24-alpine AS builder

# Install git and the C build toolchain (for plugins)
RUN apk --no-cache add git build-base

# Set the working directory
WORKDIR /app

# Copy and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# --- THIS IS THE FIX ---
# 1. Create the nested /app/plugins/plugins directory structure.
RUN mkdir -p /app/plugins/plugins

# 2. Compile each adaptor and place the .so file inside the nested directory.
RUN (cd adaptors/cfplugin && go build -buildmode=plugin -o /app/plugins/plugins/cfplugin.so .)
RUN (cd adaptors/dns-adaptor && go build -buildmode=plugin -o /app/plugins/plugins/dns-adaptor.so .)
RUN (cd adaptors/hana-adaptor && go build -buildmode=plugin -o /app/plugins/plugins/hana-adaptor.so .)
# --- END OF FIX ---

# Build the main application
RUN go build -a -installsuffix cgo -o /app/mrm-cell .


# --- Stage 2: The Final Image ---
FROM golang:1.24-alpine

WORKDIR /app

# Copy the compiled application binary from the 'builder' stage
COPY --from=builder /app/mrm-cell .

# Copy the config and telemetry files
COPY fsm-config.yaml .
COPY telemetry.yaml .

# Copy the entire /app/plugins directory from the builder.
# This will result in a /app/plugins/plugins structure in the final image.
COPY --from=builder /app/plugins/ ./plugins/

# Expose the necessary ports
EXPOSE 9081 8082 8083 2379 2380 

# The command to run when a container is started
CMD ["./mrm-cell"]
