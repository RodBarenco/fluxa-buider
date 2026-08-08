# Manual do Usuário — Fluxa Builder

*[Also available in English: user-manual.md](user-manual.md)*

Este manual mostra o caminho atual para transformar um projeto Fluxa já
testado em uma aplicação portátil que pode ser aberta diretamente pelo usuário
final.

> **Estado atual:** Linux e Windows produzem pacotes portáteis funcionais.
> O macOS produz um `.app` funcional para testes, mas a distribuição pública
> ainda precisa de assinatura Developer ID e notarização da Apple.

## Caminho guiado: `fluxa-builder init`

Depois de compilar o Builder (seção 2) e testar o projeto Fluxa manualmente
(seção 3), o comando interativo abaixo ajuda nas seções 4, 5 e 6 sem exigir
que você já saiba de cor todos os comandos e campos:

```sh
fluxa-builder init
```

Ele detecta o sistema automaticamente e, na ordem, pergunta:

1. o diretório do projeto (caminho completo). Se `fluxa.toml` estiver
   ausente ou faltar `name`, `id`, `version` ou `entry`, o wizard oferece
   preencher cada campo — sempre mostrando a linha exata antes de gravar e
   pedindo confirmação;
2. qual plataforma gerar. Escolher a plataforma da própria máquina continua
   funcionando como sempre. Escolher `windows-x64` ou `linux-x64` em outra
   máquina agora também funciona de verdade: o teste de fumaça que precisa
   passar antes de publicar roda dentro de um container Docker isolado de
   rede, em vez de nativamente — ver "Construindo para outra plataforma" na
   seção 6. Escolher `macos` numa máquina que não é Mac, ou "as três
   plataformas" numa única execução, ainda não é suportado; o wizard explica
   o motivo e pergunta de novo, e uma resposta que não corresponde a nenhuma
   opção do menu é recusada em vez de virar silenciosamente uma build da
   própria máquina. O que você escolher aqui vale mais do que qualquer
   `build.target` já fixado no `fluxa.toml`;
3. as configurações opcionais mais comuns descritas na seção 4 —
   `build.assets`, `build.exclude`, `build.persistent`/`build.export`,
   `package.include_source`, e o ícone/`bundle_id` **da plataforma escolhida
   acima**, não da máquina em que você está — pulando silenciosamente
   qualquer campo que já esteja definido no `fluxa.toml`. É por isso que a
   pergunta do alvo vem primeiro: cada build lê apenas o ícone do próprio
   alvo, então uma build para Windows pede o `.ico` que ela realmente vai
   embutir;
4. onde salvar a saída, com a opção de gravar essa escolha em
   `build.output`;
5. se deve tentar baixar e compilar o toolchain da Fluxa automaticamente.
   No Linux e no Windows, respondendo "sim" o wizard clona o `fluxa-lang` e
   compila de verdade dentro de um container Docker fixo (inclusive
   cross-compilando para Windows a partir do Linux, sem precisar de uma
   máquina Windows) — nunca mexe nos pacotes do sistema. Gerando para
   Windows a partir do Linux, esse mesmo download é compilado duas vezes:
   o runtime Windows que você distribui e mais um compilador que roda na
   *sua* máquina, já que um `.exe` cross-compilado não compila nada aqui.
   Recusar, ou uma condição que a automação ainda não cobre, cai no guia manual
   (equivalente às seções 5 e 6 abaixo), incluindo um `runtime.json` inicial
   já preenchido com o que for possível calcular. No macOS essa automação
   ainda está em espera e sempre cai direto no guia manual. Ver
   `docs/adr/0027-automatic-toolchain-acquisition.md` (em inglês).

O wizard pula direto para gerar a aplicação de verdade (equivalente à
seção 6) somente quando existe um toolchain `fluxa` **e** um runtime
registrado que realmente resolve para esta build — a mesma seleção que o
`build` faz, não apenas um runtime da mesma plataforma. Um runtime
registrado só serve com exatamente o toolchain, o `fluxa.libs` e o modo de
terminal contra os quais foi construído; então um runtime `windows-x64`
que sobrou de outro projeto não conta como pronto: o wizard explica isso e
oferece construir o par compatível, em vez de iniciar uma build que
morreria na seleção do runtime.

Como os slots do registro são identificados pela versão que o toolchain
reporta, e o `fluxa-lang` não reporta nenhuma, todo runtime construído hoje
cai no mesmo slot para um dado alvo e modo de terminal. Quando a aquisição
produz um runtime cujo slot já está ocupado pelo incompatível, o wizard
pergunta antes de substituir; recusar deixa o registro intacto.

## 1. Pré-requisitos

Na máquina do sistema que será empacotado, instale ou prepare:

- o toolchain Fluxa compatível com o projeto;
- Go, para compilar o Fluxa Builder;
- o código do Fluxa Builder;
- um runtime Fluxa protegido, compilado para o mesmo sistema, arquitetura e
  modo de terminal do aplicativo.

O Builder sempre executa a aplicação gerada de verdade antes de publicar.
Quando a máquina consegue rodar o alvo nativamente, esse teste roda direto;
quando não consegue — uma build `windows-x64` no Linux, ou o contrário —
ele roda dentro de um container Docker isolado de rede. Por isso esses dois
alvos podem ser gerados a partir de qualquer um dos dois sistemas. O macOS
é a exceção: precisa ser gerado no próprio macOS, porque nenhum container
executa macOS. Ver "Construindo para outra plataforma" na seção 6.

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

Como o runtime é preparado depende do sistema, porque só o Windows precisa
de um binário compilado de forma diferente:

- **Linux e macOS**: no repositório `fluxa-lang`, compile o interpretador
  nativo normal com `make build`, usando os backends necessários ao projeto
  (veja `fluxa.libs` naquele repositório) — os dois sistemas compartilham o
  mesmo `src/main.c`. Nenhum dos dois entrypoints nativos tem um modo
  privado próprio — o Fluxa Builder monta, no momento do `build`, um pequeno
  relay embutido (compilado por arquitetura) como `.fluxa-runtime` que fala
  o protocolo privado do launcher e executa esse binário registrado
  (colocado ao lado como `.fluxa-runtime.interpreter`) com o comando já
  existente `run <entry> -proj .`. Ver ADR 0025 (em inglês, `docs/adr/`). O
  relay do macOS foi cross-compilado e validado por hash, mas ainda não
  confirmado de ponta a ponta em hardware macOS real — só o Linux passou
  por esse teste até agora.
- **Windows**: compile a variante realmente empacotada com
  `make build-windows-packaged` (veja `docs/WINDOWS.md` no repositório
  `fluxa-lang`). Esse binário é compilado com `FLUXA_PACKAGED_RUNTIME=1` e já
  recusa sozinho comandos públicos como:

  ```powershell
  fluxa-runtime.exe run qualquer-arquivo.flx
  ```

Em ambos os casos, o resultado final recusa uso direto/público com código de
saída 126 — no Linux porque o relay do Builder recusa, no Windows porque o
próprio binário recusa.

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

Para gravar em um diretório diferente de `build.output` sem editar o
`fluxa.toml`, use `--output` (sujeito à mesma regra de segurança: caminho
relativo, sem `..`, permanecendo dentro do projeto):

```sh
fluxa-builder build . --include-source --output build-output
```

### Construindo para outra plataforma

`--target <os>-<arch>` gera para uma plataforma diferente da máquina atual
numa única execução, sem editar o `fluxa.toml`:

```sh
# a partir de uma máquina Linux
fluxa-builder build . --include-source --target windows-x64
```

O teste de fumaça continua executando a aplicação gerada de verdade antes
de publicar — essa garantia de segurança não é enfraquecida — mas quando a
máquina não consegue rodar aquele alvo nativamente, ele roda dentro de um
container Docker isolado de rede em vez disso. Gerar `linux-x64` dessa
forma funciona por completo. Gerar `windows-x64` dessa forma tem uma
limitação de confiabilidade conhecida e ainda não resolvida em algumas
máquinas: quando a verificação em container não consegue nem rodar (Docker
ausente, ou essa limitação), o build ainda assim publica, com uma linha
`WARNING:`, em vez de ser bloqueado. Ver
`docs/adr/0028-container-verified-cross-platform-builds.md` (em inglês)
para o status exato e o raciocínio por trás dessa escolha. Alvos `macos`
sempre precisam ser gerados em hardware Mac real; não existe caminho via
container para o macOS.

O Builder:

1. coleta somente os arquivos declarados;
2. cria e verifica o pacote FLXPKG;
3. seleciona um runtime protegido compatível;
4. monta o launcher (compilado para a plataforma alvo, não para a máquina
   que roda o build) e o runtime privado;
5. executa o teste sem abrir a interface — nativamente, ou dentro de um
   container quando a máquina não consegue executar o alvo;
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
│   ├── meu-jogo.ico                (somente se targets.windows.icon estiver definido)
│   ├── dxil.dll
│   ├── libgallium_wgl.dll
│   ├── opengl32.dll
│   ├── opengl32sw.dll
│   ├── meu-jogo.exe.local
│   ├── windows-version.json
│   └── build-info.json
├── meu-jogo.zip
└── meu-jogo.zip.sha256
```

Entregue o ZIP. O usuário extrai a pasta e abre somente `meu-jogo.exe`. Se um
ícone foi configurado, ele já fica embutido nos recursos do próprio `.exe`
(veja "Associar o ícone ao executável", abaixo) — o `.ico` solto continua
sendo entregue de qualquer forma.

Os quatro arquivos `.dll` e o `meu-jogo.exe.local` são o fallback de
renderização por software do Mesa3D — deixam `std.graph` funcionando mesmo
numa máquina sem driver de GPU utilizável (comum em VMs). São baixados uma
vez, verificados por checksum e cacheados em `~/.fluxa-builder/mesa-dist-win`;
se essa etapa falhar, o build imprime um `WARNING:` e continua sem os
arquivos — é uma melhoria de compatibilidade opcional, não um requisito
funcional. Ver `docs/adr/0027-automatic-toolchain-acquisition.md` (em
inglês).

### Linux

```text
dist/linux-x64/
├── meu-jogo/
│   ├── meu-jogo
│   ├── .fluxa-runtime
│   ├── .fluxa-runtime.interpreter
│   ├── meu-jogo.flxpkg
│   ├── meu-jogo.png                (somente se targets.linux.icon estiver definido)
│   ├── install-desktop-shortcut.sh
│   ├── build-info.json
│   └── linux-runtime.json
├── meu-jogo.tar.gz
├── meu-jogo.tar.gz.sha256
├── com.exemplo.meu-jogo_1.0.0_amd64.deb
└── com.exemplo.meu-jogo_1.0.0_amd64.deb.sha256
```

É possível entregar o `.tar.gz` portátil ou o instalador `.deb`. Os dois já
registram um ícone e uma entrada de menu automaticamente — o `.deb` na
instalação, o `.tar.gz` portátil na primeira vez que o jogo é aberto.

### Associar o ícone ao executável

Um ícone configurado (`targets.windows.icon` / `targets.linux.icon`) sempre é
entregue como um arquivo solto ao lado do executável. Ver
`docs/adr/0026-file-manager-icon-association.md` (em inglês) para o desenho
completo; resumindo:

- **Windows**: o ícone já é embutido diretamente nos recursos do `.exe`
  durante o `build` — nenhuma etapa extra é necessária. Isso é feito de
  forma best-effort: se o launcher não tiver espaço de cabeçalho para mais
  uma seção, ou já carregar recursos embutidos, o `fluxa-builder build`
  imprime uma linha `WARNING:`, o build continua e é publicado normalmente,
  e o `.ico` solto continua sendo entregue como antes.
- **Linux**: o próprio launcher registra sua entrada `.desktop` em
  `~/.local/share/applications` automaticamente na primeira execução —
  nenhuma etapa extra para quem só quer jogar — e atualiza essa entrada a
  cada execução seguinte, então mover a pasta depois não deixa um atalho
  desatualizado. O script `install-desktop-shortcut.sh` também continua
  sendo entregue na raiz da pasta portátil, útil para scripts de
  provisionamento ou instalação em massa que nunca chegam a abrir a
  interface gráfica; ele registra a mesma entrada e também é seguro de
  executar de novo.

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
