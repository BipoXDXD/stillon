package tenancy

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrTransferTarget: a titularidade só vai para outro owner do projeto, e o
// Projeto padrão não é transferido.
var ErrTransferTarget = errors.New("a titularidade só vai para outro owner, e o Projeto padrão fica com o titular")

// A transferência de titularidade é em dois passos (decisão D2): o titular
// propõe a um owner, e o owner que recebe aceita, levando a cota do projeto.
// Ninguém ganha cota alheia sem querer.

// AuthorizeTransferProposal diz se actor pode propor a titularidade do
// projeto (titular titularID, isDefault se é o Projeto padrão) a target. Só o
// titular propõe; o alvo tem de ser outro membro com papel owner. O Projeto
// padrão não é transferido: quem recebe já tem o dele (índice único
// projects_um_padrao_por_titular), e as rotas antigas dependem do padrão de
// cada um. A CHECK projects_padrao_nao_transfere (00051) é a segunda barreira.
// Isso pode ser revisto pela D4 do PR 7.
func AuthorizeTransferProposal(actor ProjectScope, titularID uuid.UUID, isDefault bool, target MemberView) error {
	if actor.UserID != titularID {
		return fmt.Errorf("%w: só o titular propõe a transferência", ErrForbidden)
	}
	if isDefault || target.UserID == titularID || target.Role != RoleOwner {
		return ErrTransferTarget
	}
	return nil
}

// AuthorizeTransferAcceptance diz se actor pode aceitar a titularidade
// proposta a pendingID (uuid.Nil sem proposta): só o próprio pendente, e
// ainda owner. Rebaixar ou remover o pendente já apaga a proposta; o papel é
// conferido de novo como defesa, e a FK projects_titular_e_owner (00048) é a
// barreira do banco.
func AuthorizeTransferAcceptance(actor ProjectScope, pendingID uuid.UUID) error {
	if pendingID == uuid.Nil || actor.UserID != pendingID {
		return fmt.Errorf("%w: a titularidade não foi proposta a quem aceita", ErrForbidden)
	}
	return actor.Require(PermManageMembers)
}

// AuthorizeTransferCancel diz se actor pode desfazer a proposta: o titular
// cancela (sem proposta, não há o que desfazer, e passa) e o pendente recusa.
func AuthorizeTransferCancel(actor ProjectScope, titularID, pendingID uuid.UUID) error {
	if actor.UserID == titularID || (pendingID != uuid.Nil && actor.UserID == pendingID) {
		return nil
	}
	return fmt.Errorf("%w: só o titular ou quem recebe desfaz a proposta", ErrForbidden)
}
