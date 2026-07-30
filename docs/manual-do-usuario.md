# Manual do Usuário — Fluxa Builder

Este manual mostra o caminho atual para transformar um projeto Fluxa já
testado em uma aplicação portátil que pode ser aberta diretamente pelo usuário
final.

> **Estado atual:** Linux e Windows produzem pacotes portáteis funcionais.
> O macOS produz um `.app` funcional para testes, mas a distribuição pública
> ainda precisa de assinatura Developer ID e notarização da Apple.

## 1. Pré-requisitos

Na máquina do sistema que será empacotado, instale ou prepare:

- o toolchain Fluxa compatível com o projeto;
- Go, para compilar o Fluxa Builder;
- o código do Fluxa Builder;
- um runtime Fluxa protegido, compilado para o mesmo sistema, arquitetura e
  modo de terminal do aplicativo.

O Builder executa um teste nativo antes de publicar. Por isso, gere o pacote
Windows no Windows, o pacote macOS no macOS e o pacote Linux no Linux.

## 2. Compilar o Builder

Linux ou macOS:

```sh
go build -trimpath -o bin/fluxa-builder ./cmd/fluxa-builder
./bin/fluxa-builder version
```

Windows PowerShell:

```powershell
go build -trimpath -o bin/fluxa-builder.exe ./cmd/fluxa-builder
.\bin\fluxa-builder.exe version
```

## 3. Testar o projeto Fluxa

O Builder não executa o preflight porque esse recurso ainda pode rejeitar
programas Fluxa válidos. Antes de empacotar, entre na pasta do projeto e execute:

```sh
fluxa run main.flx -proj .
```

Teste as telas, arquivos, banco de dados, áudio, imagens e demais recursos do
programa. O Builder pressupõe que o projeto já passou nesse teste.

## 4. Configurar `fluxa.toml`

Exemplo para um jogo gráfico:

```toml
[project]
name = "Meu Jogo"
id = "com.exemplo.meu-jogo"
version = "1.0.0"
entry = "main.flx"
type = "desktop"

[toolchain]
path = "fluxa"

[build]
output = "dist"
target = "host"
terminal = false
assets = [
  "fluxa.toml",
  "fluxa.libs",
  "assets/**",
  "languages/**",
]
persistent = ["jogo.db", "cards/**"]
export = ["cards/**"]

[package]
format = "portable"
compress = true
include_source = true

[targets.windows]
icon = "assets/app.ico"

[targets.linux]
icon = "assets/app.png"

[targets.macos]
icon = "assets/AppIcon.icns"
bundle_id = "com.exemplo.meu-jogo"
```

Principais opções:

- `terminal = false`: aplicativo gráfico sem janela de terminal no Windows;
- `terminal = true`: aplicativo que utiliza console;
- `assets`: arquivos que serão entregues com o programa;
- `persistent`: banco, configurações e arquivos gerados que devem sobreviver
  às próximas execuções;
- `export`: subconjunto de `persistent` que também ficará visível ao usuário;
- `include_source = true`: necessário enquanto Fluxa ainda não exporta
  bytecode distribuível estável.

Os padrões de `export` precisam aparecer exatamente em `persistent`.

Um banco SQLite gerado pelo programa não precisa existir antes do build. Se o
programa cria o arquivo e suas tabelas na primeira execução, não inclua um banco
já jogado em `assets`.

## 5. Preparar o runtime protegido

No repositório da linguagem Fluxa, gere a variante de distribuição com os
backends necessários ao projeto. Exemplo:

```sh
make FLUXA_GRAPH_RAYLIB=1 build-packaged
```

Essa variante deve ter sido compilada com `FLUXA_PACKAGED_RUNTIME=1`. Ela aceita
o protocolo privado do launcher, mas recusa comandos públicos como:

```sh
fluxa-runtime run qualquer-arquivo.flx
```

O runtime precisa corresponder ao alvo:

| Sistema | Arquitetura | Nome do runtime |
|---|---|---|
| Linux x64 | `amd64` | `fluxa-runtime` |
| Windows x64 | `amd64` | `fluxa-runtime.exe` |
| macOS Intel | `amd64` | `fluxa-runtime` |
| macOS Apple Silicon | `arm64` | `fluxa-runtime` |

Também é necessário um `runtime.json` no formato descrito em
[Project Configuration](configuration.md#runtime-registry). Os hashes devem
identificar exatamente o toolchain, o `fluxa.libs` e o runtime usados.

Registre o runtime:

```sh
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
fluxa-builder runtime list
```

No Windows:

```powershell
.\fluxa-builder.exe runtime add .\fluxa-runtime.exe --metadata .\runtime.json
.\fluxa-builder.exe runtime list
```

Por padrão, o registro fica em `~/.fluxa-builder/runtimes`. Para usar outro:

```sh
fluxa-builder runtime add ./fluxa-runtime \
  --metadata ./runtime.json \
  --registry ./meus-runtimes
```

## 6. Gerar a aplicação

Execute na raiz do projeto:

```sh
fluxa-builder build . --include-source
```

Se estiver usando um registro alternativo:

```sh
fluxa-builder build . \
  --include-source \
  --runtime-registry ./meus-runtimes
```

O Builder:

1. coleta somente os arquivos declarados;
2. cria e verifica o pacote FLXPKG;
3. seleciona um runtime protegido compatível;
4. monta o launcher e o runtime privado;
5. executa o teste nativo sem abrir a interface;
6. cria o arquivo de distribuição e seu SHA-256;
7. publica o resultado somente se todas as etapas passarem.

O Builder não sobrescreve uma saída já existente. Antes de repetir uma build,
renomeie ou remova conscientemente a pasta antiga do alvo em `dist`.

## 7. Resultado por sistema

### Windows

O resultado fica normalmente em:

```text
dist/windows-x64/
├── meu-jogo/
│   ├── meu-jogo.exe
│   ├── .fluxa-runtime.exe
│   ├── meu-jogo.flxpkg
│   ├── windows-version.json
│   └── build-info.json
├── meu-jogo.zip
└── meu-jogo.zip.sha256
```

Entregue o ZIP. O usuário extrai a pasta e abre somente `meu-jogo.exe`.

### Linux

```text
dist/linux-x64/
├── meu-jogo/
├── meu-jogo.tar.gz
├── meu-jogo.tar.gz.sha256
├── com.exemplo.meu-jogo_1.0.0_amd64.deb
└── com.exemplo.meu-jogo_1.0.0_amd64.deb.sha256
```

É possível entregar o `.tar.gz` portátil ou o instalador `.deb`.

### macOS

```text
dist/macos-x64/
├── meu-jogo.app/
├── meu-jogo.app.tar.gz
└── meu-jogo.app.tar.gz.sha256
```

O `.app` contém o launcher público, o runtime privado e o FLXPKG. O artefato
sem assinatura serve para desenvolvimento. Para distribuição pública a
usuários não técnicos, ainda será necessário assinar todos os componentes com
Developer ID, ativar Hardened Runtime, notarizar e anexar o ticket da Apple.

## 8. Teste manual antes de distribuir

Teste a aplicação exatamente como o usuário final:

1. copie apenas o ZIP, arquivo portátil ou instalador final para outro local;
2. extraia ou instale;
3. abra somente o executável com o nome do aplicativo;
4. confirme que nenhum terminal aparece quando `terminal = false`;
5. teste imagens, fontes, áudio, vídeos, idiomas e banco de dados;
6. gere um save ou arquivo exportado;
7. feche o programa normalmente;
8. abra novamente e confirme que o estado foi preservado;
9. confira os arquivos exportados, como a pasta `cards`;
10. tente executar o runtime privado com um arquivo `.flx` arbitrário e
    confirme que ele recusa a operação.

Os dados internos ficam em:

```text
Linux:   $XDG_DATA_HOME/fluxa/<project.id>/project
Windows: %AppData%\fluxa\<project.id>\project
macOS:   ~/Library/Application Support/fluxa/<project.id>/project
```

Quando a pasta do aplicativo for gravável, os itens de `build.export` aparecem
ao lado dele. Em instalações protegidas, o fallback é a pasta
`Documentos/<nome do projeto>`.

## 9. Limitações atuais

- Os arquivos `.flx` ainda são incluídos como fonte legível.
- O runtime protegido impede o uso normal/acidental como CLI, mas não é DRM.
- Assinatura Ed25519 do FLXPKG não substitui Authenticode no Windows nem
  Developer ID/notarização no macOS.
- O macOS ainda não gera uma distribuição pública notarizada.
- O Windows ainda não possui instalador nativo nem assinatura Authenticode.

Para todas as opções avançadas, consulte
[Project Configuration](configuration.md) e
[Distribution Guide](distribution.md).
