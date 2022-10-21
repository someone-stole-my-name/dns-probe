ARG GOVERSION=1.19
FROM golang:${GOVERSION} AS build

WORKDIR /app
COPY . .
RUN make build

FROM scratch
COPY --from=build /app/out/bin/dns-probe /dns-probe
ENTRYPOINT ["/dns-probe"]

