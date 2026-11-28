package tenancy

// InvitationHashForTest expõe o hash do link aos testes externos.
func InvitationHashForTest(token string) []byte { return invitationHash(token) }
