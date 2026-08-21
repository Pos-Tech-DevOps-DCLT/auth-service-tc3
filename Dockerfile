# ── Estágio de build ────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Baixa dependências primeiro (melhor cache)
COPY go.mod go.sum ./
RUN go mod download

# Copia o código e compila binário estático
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o auth-service .

# ── Estágio final (imagem mínima) ────────────────────────────────────────────
FROM scratch

WORKDIR /app

# Copia apenas o binário compilado
COPY --from=builder /app/auth-service .

EXPOSE 8001

CMD ["./auth-service"]
