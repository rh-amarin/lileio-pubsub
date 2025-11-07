# Example

## NATS Examples

The following examples currently run Nats Streaming Server, so you can first run that using Docker

```
docker run -v $(pwd)/data:/datastore -p 4222:4222 -p 8223:8223 nats-streaming -p 4222 -m 8223 -store file -dir /datastore
```

Then you can run the example subscriber and publisher..

```
go run example/nats-subscriber/main.go
```

```
go run example/nats-publisher/main.go
```

## RabbitMQ Examples

The following examples require RabbitMQ to be running locally. You can start RabbitMQ using Docker:

```
docker run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3-management
```

Then you can run the example subscriber and publisher..

```
go run example/rabbitmq-subscriber/main.go
```

```
go run example/rabbitmq-publisher/main.go
```

The default connection URL is `amqp://guest:guest@127.0.0.1:5672/` (using IPv4 to avoid IPv6 connection issues). You can customize it by modifying the examples or setting the `RABBITMQ_URL` environment variable.

**Note:** If you encounter connection errors, make sure RabbitMQ is running and accessible. The examples use `127.0.0.1` instead of `localhost` to avoid IPv6 resolution issues.
