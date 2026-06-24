# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN if [ "$TARGETARCH" = "arm" ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    version="$(tr -d '[:space:]' < VERSION)"; \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags="-s -w -X github.com/yuzumone/org-syncd/internal/cli.version=${version}" -o /out/org-syncd ./cmd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/org-syncd /usr/local/bin/org-syncd
EXPOSE 8080
ENTRYPOINT ["org-syncd"]
CMD ["serve"]
