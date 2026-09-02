package main

// The list is the program.
//
// What an archive carries that nobody meant to send, and what a name means
// when it turns up in one. Every entry says what it is and why it matters,
// and there is a test that refuses to let one exist without a reason.

import (
	"path"
	"strings"
)

type Place struct {
	Match string
	What  string
	Why   string
}

// grave is what should not be in an archive that leaves a machine.
var grave = []Place{
	{"id_rsa", "a private ssh key",
		"whoever has the archive can log in wherever that key is trusted"},
	{"id_ed25519", "a private ssh key",
		"whoever has the archive can log in wherever that key is trusted"},
	{"id_dsa", "a private ssh key",
		"whoever has the archive can log in wherever that key is trusted"},
	{"id_ecdsa", "a private ssh key",
		"whoever has the archive can log in wherever that key is trusted"},
	{".env", "an environment file",
		"the file an application reads its secrets out of"},
	{".npmrc", "an npm configuration",
		"if there is a token in it, whoever holds it can publish under that name"},
	{".netrc", "a netrc",
		"logins in plain text for whatever machines it lists"},
	{".pypirc", "a PyPI configuration", "uploading packages under that name"},
	{"credentials", "a credentials file",
		"the name aws, gcloud and half a dozen others use for exactly that"},
	{".git-credentials", "git logins", "acting as somebody on whatever forge it names"},
	{"kubeconfig", "a Kubernetes configuration",
		"reaching the clusters it lists, as whoever it says"},
	{".pem", "a key or a certificate",
		"worth opening: holding the private half is being the server"},
	{".p12", "a key store",
		"a bundle with the private key in it, behind a password nobody changed"},
	{".pfx", "a key store",
		"a bundle with the private key in it, behind a password nobody changed"},
	{".jks", "a Java key store", "the default password is changeit and it usually still is"},
	{".kdbx", "a password database", "everything in it, behind one password"},
	{"shadow", "the password hashes of a machine",
		"every account on it, given time and a wordlist"},
	{".bash_history", "a shell history",
		"every command somebody typed, including the ones with a password in them"},
	{".zsh_history", "a shell history",
		"every command somebody typed, including the ones with a password in them"},
	{"wallet.dat", "a wallet file", "the funds in that wallet"},
	{".ssh/config", "an ssh configuration",
		"the list of machines somebody logs in to and how"},
}

// tagalong is what comes along without anybody choosing it. None of it is
// a secret and all of it says something about the machine that made the
// archive.
var tagalong = []Place{
	{"__MACOSX/", "the resource forks a Mac adds",
		"a shadow copy of every file, made by the Finder and invisible on a Mac"},
	{".DS_Store", "a Finder folder record",
		"it lists the names of everything in that folder, including what has been deleted since"},
	{"Thumbs.db", "a Windows thumbnail cache",
		"it holds small pictures of images that were in the folder, including ones no longer there"},
	{"desktop.ini", "a Windows folder setting", "nothing much, and it was not meant to travel"},
	{":Zone.Identifier", "the mark of where a file was downloaded from",
		"it carries the address the file came from originally"},
	{".git/", "a whole git repository",
		"the entire history, every branch and every author, not just the files"},
	{".svn/", "a subversion working copy", "the history and the server it came from"},
	{".idea/", "JetBrains project settings", "local paths and the layout of somebody's machine"},
	{".vscode/", "editor settings",
		"worth a look on its own: tasks in here can run when the folder is opened"},
	{"node_modules/", "an installed dependency tree",
		"weight, and thousands of files nobody in the chain has read"},
	{".venv/", "a python virtual environment", "weight, and paths from the machine that built it"},
	{".terraform/", "terraform state or providers", "it can carry the state of what it manages"},
	{".pytest_cache/", "test run leftovers", "nothing much, and it was not meant to travel"},
	{"__pycache__/", "compiled python", "it carries the absolute path of the source it came from"},
	{".pyc", "compiled python", "it carries the absolute path of the source it came from"},
}

// alsoArchive is an archive inside an archive, which is where a scanner
// that only looks one level down stops.
var alsoArchive = map[string]bool{
	".zip": true, ".jar": true, ".war": true, ".apk": true, ".tar": true,
	".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true,
	".rar": true, ".iso": true, ".cab": true, ".whl": true, ".egg": true,
	".nupkg": true, ".vsix": true, ".crx": true, ".deb": true, ".rpm": true,
}

// named looks a path up in one of the lists above.
func named(list []Place, name string) (Place, bool) {
	tidy := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(tidy)
	best := Place{}
	ok := false

	for _, p := range list {
		match := strings.ToLower(p.Match)
		hit := false
		switch {
		case strings.HasSuffix(match, "/"):
			hit = strings.Contains("/"+tidy, "/"+match)
		case strings.HasPrefix(match, "."):
			hit = base == match || strings.HasSuffix(base, match)
		default:
			hit = base == match || strings.Contains(tidy, "/"+match)
		}
		if hit && (!ok || len(match) > len(best.Match)) {
			best, ok = p, true
		}
	}
	return best, ok
}

// isNested says whether an entry is itself an archive.
func isNested(name string) bool {
	return alsoArchive[strings.ToLower(path.Ext(name))]
}
