# Native dev loop: `tilt up`. Tokens come from .env, as in docker-compose.yml.
run = 'set -a; . ./.env; set +a; eval "$(make env)"; '
src = ['cmd', 'services', 'internal', 'gen', 'go.mod', 'go.sum']

# whisper.cpp, once; make skips it when libwhisper.a exists.
local_resource('lib', 'make lib', labels=['go'])

local_resource(
    'app',
    serve_cmd='yarn workspace castor-app dev',
    resource_deps=['install', 'api-server'],
    links=['http://localhost:3000'],
    labels=['app'],
)

local_resource(
    'app-generate',
    'yarn workspace castor-app generate',
    deps=['proto'],
    resource_deps=['install'],
    labels=['app'],
)

local_resource(
    'api-server',
    serve_cmd=run + 'CASTOR_SERVER__URL=http://localhost:8410 exec go run ./cmd/castor api-server',
    deps=src,
    ignore=['**/*_test.go'],
    resource_deps=['media-server'],
    links=['http://localhost:8411'],
    labels=['go'],
)

local_resource(
    'media-server',
    serve_cmd=run + 'exec go run ./cmd/castor media-server',
    deps=src,
    ignore=['**/*_test.go'],
    resource_deps=['lib'],
    links=['http://localhost:8410'],
    labels=['go'],
)

local_resource(
    'install',
    'yarn install',
    deps=['package.json', 'app/package.json'],
    labels=['app'],
)