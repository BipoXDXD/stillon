# Só o ensaio SSH: nenhuma chave, daemon do Docker ou aplicação nesta imagem.
FROM ubuntu:25.10@sha256:7cc5e35f6567ee8c66d2abb4aab0fd866669e6207c237c3a8f0947a5c7f17092

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        openssh-server openssh-client sudo ca-certificates \
    && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
    && useradd --create-home --shell /bin/sh deploy \
    && passwd --delete deploy

COPY iniciar-servidor.sh cliente.sh entrada.sh /usr/local/lib/stillon-ssh/
COPY sshd_config /usr/local/lib/stillon-ssh/sshd_config
RUN chmod 755 /usr/local/lib/stillon-ssh/*.sh
