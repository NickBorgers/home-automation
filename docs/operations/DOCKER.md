# Docker Guide for Home Automation Go Application

This guide explains how to build, run, and deploy the home automation Go application using Docker.

## Quick Start

### 1. Build the Docker Image

From the repository root:

```bash
make docker-build-go
```

Or directly with Docker:

```bash
docker build -t homeautomation:latest ./homeautomation-go/
```

### 2. Run the Container Locally

First, create your `.env` file:

```bash
cd homeautomation-go
cp .env.example .env
# Edit .env with your Home Assistant credentials
```

Then run:

```bash
make docker-run-go
```

Or directly with Docker:

```bash
docker run --rm -it \
  --name homeautomation \
  --env-file homeautomation-go/.env \
  homeautomation:latest
```

## Docker Image Details

### Multi-Stage Build

The Dockerfile uses a multi-stage build for optimal image size:

1. **Builder stage**: Uses `golang:1.24-alpine` to compile the Go binary
2. **Runtime stage**: Uses minimal `alpine:latest` with only the compiled binary

### Image Size

- **Final image**: ~20-30MB (Alpine base + static binary)
- **Builder image**: ~500MB (includes Go toolchain, discarded after build)

### Security Features

- Runs as non-root user (`homeautomation:homeautomation` UID/GID 1000)
- Static binary (no dynamic dependencies)
- Minimal attack surface (Alpine base with only CA certificates and timezone data)

### Supported Platforms

The GitHub Actions workflow builds for multiple architectures:

- `linux/amd64` (x86_64)
- `linux/arm64` (ARM 64-bit, e.g., Raspberry Pi 4)

## GitHub Container Registry (GHCR)

### Automated Builds

Docker images are automatically built and pushed to GHCR when:

- Code is pushed to `main` or `master` branch
- A version tag is pushed (e.g., `v1.0.0`)
- Manually triggered via GitHub Actions

### Image Tags

Images are tagged with:

- `latest` - Latest build from default branch
- `main` or `master` - Latest build from that branch
- `v1.0.0` - Semantic version tags
- `v1.0` - Major.minor version
- `v1` - Major version only
- `<branch>-<sha>` - Commit-specific tags

### Pulling from GHCR

```bash
# Pull latest version
docker pull ghcr.io/nickborgersonlowsecuritynode/home-automation:latest

# Pull specific version
docker pull ghcr.io/nickborgersonlowsecuritynode/home-automation:v1.0.0

# Run from GHCR
docker run --rm -it \
  --name homeautomation \
  --env-file .env \
  ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
```

### Authentication

GHCR images may require authentication:

```bash
# Authenticate with GitHub token
echo $GITHUB_TOKEN | docker login ghcr.io -u YOUR_USERNAME --password-stdin
```

## Manual Push to GHCR

To manually push to GHCR:

```bash
# 1. Build the image
make docker-build-go

# 2. Authenticate (if not already)
echo $GITHUB_TOKEN | docker login ghcr.io -u YOUR_USERNAME --password-stdin

# 3. Push to GHCR
make docker-push-go
```

## Environment Variables

The container requires these environment variables (via `.env` file or `-e` flags):

| Variable | Required | Description | Example |
|----------|----------|-------------|---------|
| `HA_URL` | Yes | Home Assistant WebSocket URL | `wss://homeassistant.local/api/websocket` |
| `HA_TOKEN` | Yes | Long-lived access token | `eyJ0eXAiOiJKV1QiLCJhbGc...` |
| `READ_ONLY` | No | Run in read-only mode | `true` or `false` (default: `false`) |
| `HA_API_TOKEN` | No | Token for write API endpoints (e.g. `POST /api/notify`). Pass as `Authorization: Bearer <token>` or `X-HA-API-Token` header. | `long random secret` |

### Example .env File

```env
HA_URL=wss://homeassistant.local/api/websocket
HA_TOKEN=your_long_lived_access_token_here
READ_ONLY=true
HA_API_TOKEN=replace_with_a_long_random_secret
```

## Running in Production

### Docker Compose

Create a `docker-compose.yml`:

```yaml
version: '3.8'

services:
  homeautomation:
    image: ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
    container_name: homeautomation
    restart: unless-stopped
    env_file:
      - .env
    # Optional: Mount for logs
    volumes:
      - ./logs:/app/logs
    # Optional: Add health check when implemented
    # healthcheck:
    #   test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health"]
    #   interval: 30s
    #   timeout: 10s
    #   retries: 3
```

Run with:

```bash
docker-compose up -d
```

### Kubernetes

Example Kubernetes deployment:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: homeautomation-config
type: Opaque
stringData:
  HA_URL: "wss://homeassistant.local/api/websocket"
  HA_TOKEN: "your_token_here"
  READ_ONLY: "true"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: homeautomation
spec:
  replicas: 1
  selector:
    matchLabels:
      app: homeautomation
  template:
    metadata:
      labels:
        app: homeautomation
    spec:
      containers:
      - name: homeautomation
        image: ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
        envFrom:
        - secretRef:
            name: homeautomation-config
        resources:
          requests:
            memory: "32Mi"
            cpu: "100m"
          limits:
            memory: "128Mi"
            cpu: "500m"
```

## Development Workflow

### Local Development with Docker

1. **Make code changes**
2. **Rebuild image**: `make docker-build-go`
3. **Run tests**: `make unit-tests`
4. **Test container**: `make docker-run-go`

### Testing the Build

```bash
# Build image
docker build -t homeautomation:test ./homeautomation-go/

# Inspect image
docker images homeautomation:test
docker history homeautomation:test

# Run with test configuration
docker run --rm -it \
  -e HA_URL=wss://test.example.com/api/websocket \
  -e HA_TOKEN=test_token \
  -e READ_ONLY=true \
  homeautomation:test
```

### Debugging Container Issues

```bash
# Run with shell override
docker run --rm -it \
  --entrypoint /bin/sh \
  homeautomation:latest

# Check logs
docker logs homeautomation

# Inspect running container
docker exec -it homeautomation /bin/sh
```

## CI/CD Pipeline

The GitHub Actions workflow (`.github/workflows/docker-build-push.yml`) performs:

1. **Test Stage**:
   - Runs `make ci-unit-tests` (unit tests with race detector)
   - Generates coverage report
   - Fails if coverage < 65%

2. **Build and Push Stage**:
   - Builds multi-platform Docker image
   - Pushes to GHCR with appropriate tags
   - Uses GitHub Actions cache for faster builds

### Workflow Triggers

- **Push to main/master**: Builds and pushes `latest` tag
- **Push tag `v*`**: Builds and pushes version tags
- **Manual dispatch**: Allows manual workflow trigger

> **Note**: Pull requests are tested by the `pr-tests.yml` workflow. Docker images are only built and pushed from the main/master branches.

## Troubleshooting

### Build Fails

```bash
# Check Go version matches
go version  # Should be 1.24

# Clean and rebuild
make clean-go
make docker-build-go
```

### Container Won't Start

```bash
# Check environment variables
docker run --rm homeautomation:latest env

# Check if binary is executable
docker run --rm --entrypoint ls homeautomation:latest -la
```

### Connection Issues

```bash
# Test from container network
docker run --rm -it \
  --env-file .env \
  homeautomation:latest

# Check logs for specific errors
docker logs homeautomation 2>&1 | grep -i error
```

### Image Size Too Large

Current image should be ~20-30MB. If larger:

```bash
# Check layers
docker history homeautomation:latest

# Verify .dockerignore is working
docker build -t test --no-cache ./homeautomation-go/
```

## Security Best Practices

1. **Never commit `.env` files** - They contain secrets
2. **Use secrets management** - Kubernetes Secrets, Docker Secrets, or HashiCorp Vault
3. **Scan images** - Use `docker scan` or Trivy
4. **Keep base image updated** - Rebuild regularly for security patches
5. **Run as non-root** - Already configured in Dockerfile
6. **Use read-only filesystem** - Add `--read-only` flag if possible

### Scanning for Vulnerabilities

```bash
# Using Docker scan (requires Docker Hub login)
docker scan homeautomation:latest

# Using Trivy
trivy image homeautomation:latest
```

## Performance Tuning

### Resource Limits

Recommended resource limits:

```bash
docker run --rm -it \
  --memory="128m" \
  --cpus="0.5" \
  --env-file .env \
  homeautomation:latest
```

### Build Cache

The GitHub Actions workflow uses BuildKit cache:

- First build: ~2-3 minutes
- Cached builds: ~30-60 seconds

To use cache locally:

```bash
# Enable BuildKit
export DOCKER_BUILDKIT=1

# Build with cache
docker build \
  --cache-from ghcr.io/nickborgersonlowsecuritynode/home-automation:latest \
  -t homeautomation:latest \
  ./homeautomation-go/
```

## Makefile Commands Reference

| Command | Description |
|---------|-------------|
| `make docker-build-go` | Build Docker image |
| `make docker-run-go` | Build and run container |
| `make docker-push-go` | Push to GHCR |
| `make unit-tests` | Run unit tests with caching |
| `make build-go` | Build local binary |
| `make clean-go` | Clean build artifacts |

## Additional Resources

- [Dockerfile Best Practices](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/)
- [Multi-stage Builds](https://docs.docker.com/build/building/multi-stage/)
- [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [Docker Compose Documentation](https://docs.docker.com/compose/)
- [Kubernetes Documentation](https://kubernetes.io/docs/)

---

**Last Updated**: 2025-11-15
**Docker Version**: 20.10+
**BuildKit**: Recommended
