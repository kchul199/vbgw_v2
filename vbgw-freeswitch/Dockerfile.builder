FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive

# Update and install basic tools
RUN apt-get update && apt-get install -y \
    curl \
    gnupg2 \
    wget \
    lsb-release \
    git \
    build-essential \
    pkg-config \
    autoconf \
    automake \
    libtool \
    libssl-dev \
    libwebsockets-dev \
    uuid-dev \
    zlib1g-dev \
    libmagic-dev

# Add FreeSWITCH repository (Ubuntu)
RUN TOKEN=pc_9X5R1D7s0pZ1c7e2b3a4 # Public token or similar if needed, or use simple repo
RUN apt-get install -y curl && \
    curl https://freeswitch.signalwire.com/repo/deb/debian-release/signalwire-freeswitch-repo.gpg | apt-key add - && \
    echo "deb http://files.freeswitch.org/repo/deb/debian-release/ focal main" > /etc/apt/sources.list.d/freeswitch.list && \
    apt-get update && apt-get install -y freeswitch-all-dev

# Clone and build mod_audio_fork
WORKDIR /src
RUN git clone https://github.com/drachtio/drachtio-freeswitch-modules.git && \
    cd drachtio-freeswitch-modules && \
    cd modules/mod_audio_fork && \
    # Compiling manually if Makefile fails
    gcc -fPIC -shared -o mod_audio_fork.so mod_audio_fork.c -I/usr/include/freeswitch -lasound -lwebsockets -lssl -lcrypto || make

CMD ["tail", "-f", "/dev/null"]
