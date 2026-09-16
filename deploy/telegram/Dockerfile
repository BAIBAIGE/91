FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git cmake g++ make gperf libssl-dev zlib1g-dev && rm -rf /var/lib/apt/lists/*
ARG TELEGRAM_BOT_API_REV=e3e9dd8e5b3d7ab8537cd5a10dc31d5ffa8f82d1
RUN git clone https://github.com/tdlib/telegram-bot-api.git /src && cd /src && git checkout "$TELEGRAM_BOT_API_REV" && git submodule update --init --recursive
RUN cmake -S /src -B /build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr/local && cmake --build /build --target install --parallel 2

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 libssl3 zlib1g libstdc++6 && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home telegram && mkdir -p /var/lib/telegram-bot-api && chown telegram:telegram /var/lib/telegram-bot-api
COPY --from=build /usr/local/bin/telegram-bot-api /usr/local/bin/telegram-bot-api
USER telegram
WORKDIR /var/lib/telegram-bot-api
VOLUME ["/var/lib/telegram-bot-api"]
EXPOSE 7878
COPY supervisor.py /usr/local/bin/telegram-supervisor.py
ENTRYPOINT ["python3", "/usr/local/bin/telegram-supervisor.py"]
