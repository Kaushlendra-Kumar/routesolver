# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
# CGO disabled → a fully static binary that runs in a scratch/distroless image.
# The frontend is go:embed'd, so the binary is completely self-contained.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /routeserver ./cmd/routeserver

# ---- runtime stage ----
# distroless/static ships CA certificates (needed for HTTPS calls to
# Nominatim / OSRM) and a non-root user, with no shell or package manager.
FROM gcr.io/distroless/static-debian12
COPY --from=build /routeserver /routeserver
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/routeserver"]
CMD ["-addr", ":8080"]
