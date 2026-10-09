# Só o ensaio SSH: nenhuma chave, daemon do Docker ou aplicação nesta imagem.
FROM ubuntu:26.04@sha256:f144425ff09be612d6d9ad965196e9cdc23dae1f42110a8a11a3e9a8198759f7

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        openssh-server openssh-client sudo ca-certificates \
    && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
    && useradd --create-home --shell /bin/sh deploy \
    && passwd --delete deploy

COPY iniciar-servidor.sh cliente.sh entrada.sh /usr/local/lib/stillon-ssh/
COPY sshd_config /usr/local/lib/stillon-ssh/sshd_config
RUN chmod 755 /usr/local/lib/stillon-ssh/*.sh
