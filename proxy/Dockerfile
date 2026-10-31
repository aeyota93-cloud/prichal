# syntax=docker/dockerfile:1
# Прокси Telegram (telemt, github.com/telemt/telemt) в составе Причала.
# Версия закреплена, архив проверяется по sha256 из этого файла: новая версия прокси —
# это правка трёх строк ниже и обычное обновление Причала. Образ без ОС, как сам Причал.
FROM --platform=$BUILDPLATFORM alpine:3.22 AS fetch
ARG TARGETARCH
ARG TELEMT_VERSION=3.5.14
ARG TELEMT_SHA256_AMD64=71c1454f79cf755edea0985500629daba553002f9863be754e8e38a418822773
ARG TELEMT_SHA256_ARM64=21795430c4165cf964405459714d08eea319edaa5c9027beac4de373c356d84d
RUN apk add --no-cache ca-certificates curl
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) arch=x86_64; sum=$TELEMT_SHA256_AMD64 ;; \
      arm64) arch=aarch64; sum=$TELEMT_SHA256_ARM64 ;; \
      *) echo "архитектура $TARGETARCH не поддерживается" >&2; exit 1 ;; \
    esac; \
    curl -fsSL --retry 5 -o /telemt.tgz \
      "https://github.com/telemt/telemt/releases/download/${TELEMT_VERSION}/telemt-${arch}-linux-musl.tar.gz"; \
    echo "${sum}  /telemt.tgz" | sha256sum -c -; \
    mkdir /out; tar -xzf /telemt.tgz -C /out; test -x /out/telemt

FROM scratch
COPY --from=fetch /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=fetch /out/telemt /telemt
# Не root: порт внутри контейнера 8443, наружу его отдаёт docker (PROXY_PORT).
USER 65532:65532
EXPOSE 8443
ENTRYPOINT ["/telemt", "/data/config.toml"]
