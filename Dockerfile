FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/qualification ./cmd/qualification

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /out/qualification /usr/local/bin/qualification
COPY LICENCE.md /usr/share/licenses/qualification/
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/qualification"]
CMD ["listen"]
