import { LinkTerciario } from '@/features/conta/comum'
import { Lista, MolduraLegal, Secao } from './MolduraLegal'

// Os Termos de Uso. Mudar o texto é mudar a versão (versao.json e
// internal/legal), e o operador continua "[operador]" até o dono aprovar.
export function PaginaTermos() {
  return (
    <MolduraLegal titulo="Termos de Uso">
      <p>
        Estes termos são o acordo entre você e [operador], que opera o StillOn. Ao criar a conta e marcar que aceita,
        você concorda com eles e com a{' '}
        <LinkTerciario para="/privacidade">Política de Privacidade</LinkTerciario>. Se não concordar, não crie a conta.
      </p>

      <Secao titulo="O serviço">
        <p>
          O StillOn é um monitor de disponibilidade: verifica de tempos em tempos os endereços que você cadastra, recebe
          os sinais (heartbeats) das suas tarefas agendadas, registra incidentes, avisa pelos canais que você configura
          e publica status pages, se você quiser.
        </p>
        <p>
          Hoje o StillOn é gratuito e não existe pagamento. Se isso mudar, avisaremos antes, e nada será cobrado sem o
          seu acordo.
        </p>
      </Secao>

      <Secao titulo="Quem pode usar">
        <p>
          O StillOn é para maiores de 18 anos. Quem tem menos de 18 anos só pode usá-lo com o consentimento e o
          acompanhamento do responsável legal, que responde pelo uso. Você precisa informar um e-mail que é seu e que
          você consegue ler.
        </p>
      </Secao>

      <Secao titulo="Sua conta">
        <Lista>
          <li>Guarde a sua senha e não a compartilhe. Recomendamos ligar a verificação em duas etapas.</li>
          <li>Você responde pelo que é feito com a sua conta.</li>
          <li>
            Se suspeitar de acesso indevido, troque a senha, encerre as outras sessões e fale com o [contato do
            operador].
          </li>
        </Lista>
      </Secao>

      <Secao titulo="Uso permitido">
        <p>Você se compromete a:</p>
        <Lista>
          <li>monitorar só endereços que são seus ou que você tem permissão para monitorar;</li>
          <li>
            não usar o StillOn para atacar, sobrecarregar ou varrer sistemas de terceiros, nem para burlar os limites da
            sua conta;
          </li>
          <li>não tentar acessar dados de outras pessoas nem contornar as proteções do serviço;</li>
          <li>não publicar em status pages conteúdo ilegal, enganoso ou que viole direitos de terceiros.</li>
        </Lista>
      </Secao>

      <Secao titulo="O que você cadastra">
        <p>
          O que você cadastra continua seu. Você nos autoriza a guardar e usar esse conteúdo só para prestar o serviço,
          como a Política de Privacidade descreve. Uma status page é pública: qualquer pessoa com o endereço dela vê o que
          você escolher mostrar.
        </p>
      </Secao>

      <Secao titulo="Sem garantia de disponibilidade">
        <p>
          O StillOn é oferecido como está. Não prometemos disponibilidade mínima (SLA), nem que todo problema será
          detectado, nem que todo aviso chegará a tempo. Não dependa só do StillOn para proteger algo crítico.
        </p>
      </Secao>

      <Secao titulo="Responsabilidade">
        <p>
          Nos limites da lei, [operador] não responde por prejuízos causados por falhas, atrasos ou interrupções do
          serviço, nem pelo uso que você faz dele. Nada nestes termos afasta os direitos que a lei garante a você,
          inclusive como consumidor.
        </p>
      </Secao>

      <Secao titulo="Suspensão e encerramento">
        <Lista>
          <li>
            Você pode excluir a sua conta quando quiser, pelo painel (Configurações) ou pelo [contato do operador].
          </li>
          <li>
            Podemos suspender ou encerrar uma conta que viole estes termos, avisando pelo e-mail da conta sempre que
            possível.
          </li>
          <li>
            Se o StillOn for encerrado, avisaremos pelo e-mail da conta, com antecedência sempre que possível, para você
            exportar os seus dados.
          </li>
        </Lista>
      </Secao>

      <Secao titulo="Mudanças nestes termos">
        <p>
          Quando estes termos mudarem, a versão nova é publicada nesta página com a data nova, e o cadastro de contas
          novas passa a pedir o aceite dela.
        </p>
      </Secao>

      <Secao titulo="Lei e foro">
        <p>
          Estes termos seguem a lei brasileira. Se você usa o StillOn como consumidor, uma disputa pode ser levada ao
          foro do seu domicílio.
        </p>
      </Secao>

      <Secao titulo="Contato">
        <p>Dúvidas sobre estes termos: [contato do operador].</p>
      </Secao>
    </MolduraLegal>
  )
}
