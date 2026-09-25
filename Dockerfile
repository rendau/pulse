FROM alpine:latest

RUN apk add --no-cache --upgrade ca-certificates tzdata curl

WORKDIR /app

COPY ./cmd/build/. ./
COPY ./migrations ./migrations
COPY ./conf.example.yml ./conf.yml

CMD ["./svc"]
