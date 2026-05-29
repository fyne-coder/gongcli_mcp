FROM golang:1.26.3-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY deploy/lab-auth/shim/main.go ./deploy/lab-auth/shim/main.go
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lab-auth-shim ./deploy/lab-auth/shim

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/lab-auth-shim /usr/local/bin/lab-auth-shim
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/lab-auth-shim"]
