FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/aurforge ./cmd/aurforge

FROM archlinux:base

RUN pacman -Syu --noconfirm \
    && pacman -S --noconfirm --needed ca-certificates docker git pacman-contrib \
    && pacman -Scc --noconfirm

COPY --from=build /out/aurforge /usr/local/bin/aurforge
COPY scripts/install-host-cli.sh scripts/uninstall-host-cli.sh /usr/local/share/aurforge/
RUN chmod 0755 /usr/local/share/aurforge/*.sh
ENTRYPOINT ["/usr/local/bin/aurforge"]
