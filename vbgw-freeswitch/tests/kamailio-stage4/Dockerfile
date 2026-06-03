FROM debian:bookworm-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates \
      kamailio \
      kamailio-extra-modules \
    && rm -rf /var/lib/apt/lists/*

COPY kamailio.cfg /etc/kamailio/kamailio.cfg

EXPOSE 5060/udp

CMD ["kamailio", "-DD", "-E", "-f", "/etc/kamailio/kamailio.cfg"]
