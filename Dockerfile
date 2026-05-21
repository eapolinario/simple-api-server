# Multistage Dockerfile for the aggregated apiserver.
#
# Stage 1 builds the binary against a pinned Go toolchain. Stage 2 ships
# only the binary + CA bundle on distroless/static. The image is consumed
# by the e2e harness via `docker build` + `kind load docker-image`; it is
# not (yet) a release artifact.
#
# Keep this lean: no shell, no package manager, no writable layers beyond
# what distroless/static provides. If you find yourself wanting to debug
# inside the container, switch the FROM in stage 2 to :debug-nonroot
# temporarily — do not commit that.

# Pin to a recent stable Go. The exact minor doesn't need to match the
# devshell's go.mod toolchain; build reproducibility is the e2e harness's
# job, not this Dockerfile's.
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache module downloads in their own layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off → fully static binary, safe for distroless/static.
# -trimpath + -ldflags='-s -w' to keep the image small and reproducible.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags='-s -w' \
        -o /out/simple-apiserver \
        ./cmd/simple-apiserver

# distroless/static:nonroot ships:
#   - /etc/ssl/certs/ca-certificates.crt (needed for delegating authn
#     to talk back to kube-apiserver over TLS)
#   - a non-root user (uid 65532)
#   - nothing else — no shell, no libc
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/simple-apiserver /usr/local/bin/simple-apiserver
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/simple-apiserver"]
