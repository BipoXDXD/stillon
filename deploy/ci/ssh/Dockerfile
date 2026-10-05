# Só o ensaio SSH: nenhuma chave, daemon do Docker ou aplicação nesta imagem.
FROM ubuntu:24.04@sha256:008173c23f95b170204355c12626cb5a965d779a7e1283b09e9cffbb1bf33ca3

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        openssh-server openssh-client sudo ca-certificates \
    && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
    && useradd --create-home --shell /bin/sh deploy \
    && passwd --delete deploy

COPY iniciar-servidor.sh cliente.sh entrada.sh /usr/local/lib/stillon-ssh/
COPY sshd_config /usr/local/lib/stillon-ssh/sshd_config
RUN chmod 755 /usr/local/lib/stillon-ssh/*.sh
