FROM archlinux:base-devel

RUN useradd --create-home --uid 1000 --shell /bin/bash builder \
    && install -d --owner=builder --group=builder /build /output \
    && printf 'builder ALL=(ALL) NOPASSWD: /usr/bin/pacman\n' > /etc/sudoers.d/builder \
    && chmod 0440 /etc/sudoers.d/builder \
    && printf '\nPACMAN_AUTH=(sudo)\n' >> /etc/makepkg.conf

COPY entrypoint.sh /usr/local/bin/aurforge-builder
RUN chmod 0755 /usr/local/bin/aurforge-builder

USER builder:builder
WORKDIR /build
ENTRYPOINT ["/usr/local/bin/aurforge-builder"]
