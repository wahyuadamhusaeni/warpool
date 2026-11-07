# Image manajer warpool: binary Go + docker CLI (mengelola container instance
# lewat socket docker host) + binary usque (untuk registrasi akun WARP).
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -ldflags="-s -w" -o /warpool .

FROM alpine:3.20
ARG USQUE_VERSION=4.2.1
RUN apk add --no-cache docker-cli ca-certificates curl unzip \
 && curl -sL -o /tmp/usque_${USQUE_VERSION}_linux_amd64.zip "https://github.com/Diniboy1123/usque/releases/download/v${USQUE_VERSION}/usque_${USQUE_VERSION}_linux_amd64.zip" \
 && curl -sL -o /tmp/checksums.txt "https://github.com/Diniboy1123/usque/releases/download/v${USQUE_VERSION}/checksums.txt" \
 && cd /tmp && grep "usque_${USQUE_VERSION}_linux_amd64.zip" checksums.txt | sha256sum -c - \
 && unzip -o /tmp/usque_${USQUE_VERSION}_linux_amd64.zip usque -d /usr/local/bin \
 && chmod +x /usr/local/bin/usque \
 && rm -f /tmp/usque_${USQUE_VERSION}_linux_amd64.zip /tmp/checksums.txt
COPY --from=build /warpool /usr/local/bin/warpool
ENTRYPOINT ["warpool"]
CMD ["daemon"]
