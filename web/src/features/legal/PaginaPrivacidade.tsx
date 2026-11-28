import { LinkTerciario } from '@/features/conta/comum'
import { Lista, MolduraLegal, Secao } from './MolduraLegal'

// A Política de Privacidade. Os prazos saem de internal/storage/maintenance.go
// e do workflow de backup; a exclusão segue as decisões D3, D4, D5 e D8 do
// plano do PR 6. Mudar o texto é mudar a versão (versao.json e
// internal/legal), e o operador continua "[operador]" até o dono aprovar.
export function PaginaPrivacidade() {
  return (
    <MolduraLegal titulo="Política de Privacidade">
      <p>
        Esta política explica quais dados pessoais o StillOn trata, para quê, com quem eles são compartilhados, por
        quanto tempo ficam guardados e como você exerce os seus direitos, conforme a Lei Geral de Proteção de Dados
        (LGPD, Lei nº 13.709/2018).
      </p>

      <Secao titulo="Quem cuida dos seus dados">
        <p>
          O StillOn é operado por [operador], que decide como os dados são tratados (o controlador, na linguagem da
          LGPD). Para qualquer assunto sobre os seus dados, inclusive para falar com o encarregado, use o [contato do
          operador].
        </p>
      </Secao>

      <Secao titulo="Quais dados tratamos">
        <Lista>
          <li>
            <strong>Conta:</strong> o seu e-mail e a sua senha. A senha nunca é guardada como você a digitou: guardamos
            só um hash (Argon2id), de onde não dá para recuperá-la.
          </li>
          <li>
            <strong>Verificação em duas etapas</strong>, se você ligar: o segredo do aplicativo autenticador, guardado
            cifrado, e os códigos de recuperação, guardados como hash.
          </li>
          <li>
            <strong>Aceite:</strong> a versão destes termos que você aceitou e o momento do cadastro.
          </li>
          <li>
            <strong>Sessões:</strong> para você ver e encerrar os acessos à sua conta, cada sessão guarda o navegador
            usado e o endereço IP, este só como hash: um resumo feito com uma chave secreta, que não mostra o IP.
          </li>
          <li>
            <strong>Registro de segurança (auditoria):</strong> as ações importantes da conta, como o cadastro, a troca
            de senha, o bloqueio por tentativas erradas e as sessões encerradas, com a data e o IP também só como hash.
          </li>
          <li>
            <strong>Monitoramento:</strong> o que você cadastra (projetos, alvos com os endereços, cabeçalhos e regras,
            heartbeats, canais de aviso com os endereços de destino e status pages) e o que o monitor produz a partir
            disso (resultados dos checks, médias por hora, incidentes e o registro dos avisos enviados).
          </li>
        </Lista>
        <p>
          Usamos só os cookies necessários para manter a sua sessão. Não usamos cookies de publicidade nem ferramentas
          de análise de navegação.
        </p>
      </Secao>

      <Secao titulo="Para que usamos e com qual base legal">
        <Lista>
          <li>
            <strong>Prestar o serviço</strong> (execução de contrato): criar e manter a sua conta, autenticar você,
            monitorar o que você cadastrou, mandar os avisos que você configurou e os e-mails da conta, como a
            confirmação do cadastro e a redefinição de senha.
          </li>
          <li>
            <strong>Segurança</strong> (legítimo interesse): limitar tentativas, barrar robôs, manter o registro de
            auditoria e investigar abuso, para proteger você, os outros usuários e o serviço.
          </li>
          <li>
            <strong>Cumprir a lei</strong> (obrigação legal): atender a ordens de autoridades, quando houver.
          </li>
        </Lista>
        <p>Não vendemos os seus dados e não os usamos para publicidade.</p>
      </Secao>

      <Secao titulo="Com quem compartilhamos">
        <p>Só com os provedores de que o StillOn precisa para funcionar, cada um para uma finalidade:</p>
        <Lista>
          <li>
            <strong>Hostinger</strong> (hospedagem): o servidor virtual onde ficam o StillOn e o banco de dados. Guarda
            todos os dados descritos acima.
          </li>
          <li>
            <strong>Cloudflare</strong> (rede e anti-robô): fica na frente do site e repassa o tráfego até o servidor,
            vendo o seu IP e o conteúdo das requisições no caminho. O Cloudflare Turnstile confere, no cadastro e no
            pedido de nova senha, que quem envia não é um robô, e para isso recebe o seu IP e sinais do navegador.
          </li>
          <li>
            <strong>Resend</strong> (e-mail): envia os e-mails da conta e os avisos por e-mail. Recebe o endereço de
            destino e o conteúdo da mensagem.
          </li>
          <li>
            <strong>GitHub</strong> (cópias de segurança): guarda as cópias de segurança do banco, que são cifradas
            no servidor antes de sair dele.
          </li>
          <li>
            <strong>healthchecks.io</strong> (saúde do servidor): recebe só um sinal periódico de que o servidor está
            de pé, sem dados seus.
          </li>
        </Lista>
        <p>
          Alguns desses provedores podem tratar dados fora do Brasil, nos termos dos contratos deles. Os avisos que você
          configura vão para o destino que você escolheu, como um webhook do Discord ou um e-mail, e lá seguem as regras
          de quem o recebe. Dados também podem ser entregues a autoridades, quando a lei exigir.
        </p>
      </Secao>

      <Secao titulo="Por quanto tempo guardamos">
        <Lista>
          <li>A conta e o que você cadastrou, inclusive os incidentes: enquanto a conta existir.</li>
          <li>
            Resultados de cada check: cerca de 30 dias. Eles saem por mês inteiro, então um resultado pode durar até 61
            dias.
          </li>
          <li>Médias por hora dos checks: 13 meses.</li>
          <li>Registro dos avisos enviados e eventos de heartbeat: 30 dias.</li>
          <li>Sessões: até 7 dias depois de vencerem.</li>
          <li>Registro de auditoria: 12 meses.</li>
          <li>
            Cópias de segurança: até 30 dias, mais a cópia semanal do provedor de hospedagem. Elas expiram sozinhas.
          </li>
        </Lista>
      </Secao>

      <Secao titulo="Quando você exclui a conta">
        <Lista>
          <li>
            A conta e os projetos só seus são apagados, com tudo o que eles contêm. Se você é titular de um projeto que
            tem outros membros, transfira a titularidade antes de excluir a conta.
          </li>
          <li>
            O que você criou em projetos de outras pessoas continua com elas, sem ligação com você. Os canais de aviso
            que você criou cujo endereço é o e-mail da sua conta são apagados; os demais são desligados.
          </li>
          <li>
            O registro de auditoria fica anônimo, sem ligação com a sua conta, até completar os 12 meses. O hash do IP
            continua nele.
          </li>
          <li>
            As cópias de segurança ainda guardam os seus dados por até 30 dias, mais a cópia semanal do provedor de
            hospedagem, e expiram sozinhas.
          </li>
          <li>Mandamos um e-mail ao endereço da conta avisando que ela foi excluída.</li>
        </Lista>
      </Secao>

      <Secao titulo="Seus direitos">
        <p>
          Pela LGPD, você pode confirmar se tratamos os seus dados, acessá-los, corrigi-los, levá-los para outro serviço
          (portabilidade), pedir a exclusão, saber com quem os compartilhamos e se opor a um tratamento feito por
          legítimo interesse.
        </p>
        <p>
          Exportar os dados e excluir a conta: pelo painel (Configurações) ou pelo [contato do operador]. A exportação
          traz os dados da conta e dos projetos, inclusive incidentes e médias por hora, mas não o resultado de cada
          check. Para os outros pedidos, use o [contato do operador].
        </p>
        <p>
          Você também pode reclamar à Autoridade Nacional de Proteção de Dados (ANPD).
        </p>
      </Secao>

      <Secao titulo="Segurança">
        <p>
          A conexão é cifrada (HTTPS), a senha e os códigos de recuperação ficam só como hash, o IP só como hash, e você
          pode ligar a verificação em duas etapas. Nenhum sistema é infalível: se um incidente de segurança puder trazer
          risco relevante a você, avisaremos você e a ANPD, como a LGPD manda.
        </p>
      </Secao>

      <Secao titulo="Idade">
        <p>
          O StillOn é para maiores de 18 anos. Quem tem menos de 18 anos só pode usá-lo com o consentimento e o
          acompanhamento do responsável legal.
        </p>
      </Secao>

      <Secao titulo="Mudanças nesta política">
        <p>
          Quando esta política mudar, a versão nova é publicada nesta página com a data nova, e o cadastro de contas
          novas passa a pedir o aceite dela.
        </p>
      </Secao>

      <p>
        Veja também os <LinkTerciario para="/termos">Termos de Uso</LinkTerciario>.
      </p>
    </MolduraLegal>
  )
}
