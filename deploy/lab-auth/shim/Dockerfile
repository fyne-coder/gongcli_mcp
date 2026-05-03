FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY main.go .
RUN go mod init gongctl-lab-auth-shim \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lab-auth-shim .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/lab-auth-shim /usr/local/bin/lab-auth-shim
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/lab-auth-shim"]
