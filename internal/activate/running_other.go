//go:build !linux

package activate

// gameRunning: fora do Linux não há como saber; quem chama precisa avisar o
// usuário para fechar o jogo.
func gameRunning(string) bool { return false }
