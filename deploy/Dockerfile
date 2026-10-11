# The comment service: one image for serve, worker and migrate, with the
# widget and the console built in.
#
# Versions are pinned so the same commit builds the same image later.

# Must match the toolchain directive in go.mod.
ARG GO_VERSION=1.26.8
ARG NODE_VERSION=22.19.0
ARG ALPINE_VERSION=3.22

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
RUN corepack enable
WORKDIR /src
COPY web/widget/package.json web/widget/pnpm-lock.yaml web/widget/
RUN cd web/widget && pnpm install --frozen-lockfile
COPY web/ web/
RUN cd web/widget && pnpm build
# The console is built when its sources are present (web/console).
RUN if [ -f web/console/package.json ]; then cd web/console && pnpm install --frozen-lockfile && pnpm build; fi

# The compiler runs on the build machine's own platform and cross-compiles
# for the target, so an arm64 image builds without emulating arm64.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/widget/dist web/widget/dist
COPY --from=web /src/web/console/dist web/console/dist

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
    -ldflags "-s -w \
      -X github.com/kite-plus/comments/internal/buildinfo.Version=${VERSION} \
      -X github.com/kite-plus/comments/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/kite-plus/comments/internal/buildinfo.Date=${DATE}" \
    -o /out/comments ./cmd/comments

FROM alpine:${ALPINE_VERSION}

# ca-certificates for the identity service, SMTP and site verification.
RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 1719 comments

COPY --from=build /out/comments /usr/local/bin/comments

USER comments
ENV COMMENTS_HTTP_ADDR=0.0.0.0:8081
EXPOSE 8081

ENTRYPOINT ["comments"]
CMD ["serve"]
