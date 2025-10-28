# Use the official Golang image to build the application
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the go mod files
COPY go.mod ./

# Download dependencies
# RUN go mod download

# Copy the source code
# COPY *.go ./
COPY main.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o clipsync .

# Use a minimal alpine image for the final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create a non-root user
RUN adduser -D -s /bin/sh appuser

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/clipsync .

# Change ownership to the non-root user
RUN chown appuser:appuser clipsync

# Switch to the non-root user
USER appuser

# Expose the default port
EXPOSE 9000

# Command to run the application
CMD ["./clipsync", "server"]
