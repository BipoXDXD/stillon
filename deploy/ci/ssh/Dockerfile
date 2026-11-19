# Só o ensaio SSH: nenhuma chave, daemon do Docker ou aplicação nesta imagem.
FROM ubuntu:24.04@sha256:008173c23f95b170204355c12626cb5a965d779a7e1283b09e9cffbb1bf33ca3

# Só o componente main (openssh, sudo e ca-certificates estão nele): o índice do
# universe tem ~19 MB e um espelho lento no runner estourava o prazo do passo.
# Um erro de índice já aciona o espelho alternativo. Os retries por arquivo
# são curtos para que o primeiro espelho não consuma o prazo de todo o build.
RUN sed -i 's/^Components: .*/Components: main/' /etc/apt/sources.list.d/ubuntu.sources \
    && printf 'Acquire::Retries "1";\nAcquire::http::Timeout "10";\n' > /etc/apt/apt.conf.d/80-ensaio \
    && instalar() { apt-get update --error-on=any \
        && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
            openssh-server openssh-client sudo ca-certificates; } \
    && { instalar || { sed -i -E 's#https?://[a-z.]*ubuntu\.com/ubuntu/#http://mirrors.edge.kernel.org/ubuntu/#g' \
            /etc/apt/sources.list.d/ubuntu.sources && instalar; }; } \
    && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
    && useradd --create-home --shell /bin/sh deploy \
    && passwd --delete deploy

COPY iniciar-servidor.sh cliente.sh entrada.sh /usr/local/lib/stillon-ssh/
COPY sshd_config /usr/local/lib/stillon-ssh/sshd_config
RUN chmod 755 /usr/local/lib/stillon-ssh/*.sh
