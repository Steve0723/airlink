FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/relay .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/relay /usr/local/bin/relay
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["relay", "-addr", ":8080", "-data", "/data"]
