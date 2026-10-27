<?php

// Exercises every Storage method a Laravel app typically uses, through the
// same Illuminate\Filesystem\AwsS3V3Adapter that Storage::disk('s3') returns.
//
//   composer install
//   php run.php            # against http://localhost:8080, bucket "laravel"
//
// Configure with the same variables as a Laravel .env.

require __DIR__.'/vendor/autoload.php';

use Aws\S3\S3Client;
use Illuminate\Filesystem\AwsS3V3Adapter;
use League\Flysystem\AwsS3V3\AwsS3V3Adapter as S3Adapter;
use League\Flysystem\Filesystem;

$env = fn (string $k, string $d) => getenv($k) ?: $d;
$endpoint = $env('AWS_ENDPOINT', 'http://localhost:8080');
$bucket = $env('AWS_BUCKET', 'laravel');

// Mirrors Laravel's FilesystemManager::createS3Driver().
$config = [
    'driver' => 's3',
    'key' => $env('AWS_ACCESS_KEY_ID', 'local'),
    'secret' => $env('AWS_SECRET_ACCESS_KEY', 'localsecret'),
    'region' => $env('AWS_DEFAULT_REGION', 'us-east-1'),
    'bucket' => $bucket,
    'url' => $env('AWS_URL', "$endpoint/$bucket"),
    'endpoint' => $endpoint,
    'use_path_style_endpoint' => true,
    'throw' => true,
];
$client = new S3Client([
    'credentials' => ['key' => $config['key'], 'secret' => $config['secret']],
    'region' => $config['region'],
    'version' => 'latest',
    'endpoint' => $endpoint,
    'use_path_style_endpoint' => true,
]);
$adapter = new S3Adapter($client, $bucket);
$disk = new AwsS3V3Adapter(new Filesystem($adapter, $config), $adapter, $config, $client);

$failures = 0;
function check(string $name, callable $fn): void
{
    global $failures;
    try {
        $result = $fn();
        if ($result === false) {
            throw new RuntimeException('assertion failed');
        }
        echo "  \033[32m✓\033[0m $name\n";
    } catch (Throwable $e) {
        $failures++;
        echo "  \033[31m✗\033[0m $name\n      ".get_class($e).': '.strtok($e->getMessage(), "\n")."\n";
    }
}
/** @return array{int, string|false, list<string>} status, body, headers */
function http(string $method, string $url, ?string $body = null): array
{
    $ctx = stream_context_create(['http' => ['method' => $method, 'content' => $body, 'ignore_errors' => true]]);
    $out = @file_get_contents($url, false, $ctx);
    $headers = $http_response_header ?? [];
    preg_match('#HTTP/\S+ (\d+)#', $headers[0] ?? '', $m);

    return [(int) ($m[1] ?? 0), $out, $headers];
}

$root = 'compat-'.bin2hex(random_bytes(4));
echo "Laravel S3 disk against $endpoint/$bucket (prefix $root/)\n\n";

check('put / get', fn () => $disk->put("$root/hello.txt", 'Hello') && $disk->get("$root/hello.txt") === 'Hello');
check('exists / missing', fn () => $disk->exists("$root/hello.txt") && $disk->missing("$root/nope.txt"));
check('size', fn () => $disk->size("$root/hello.txt") === 5);
check('lastModified', fn () => abs($disk->lastModified("$root/hello.txt") - time()) < 60);
check('mimeType', fn () => $disk->mimeType("$root/hello.txt") === 'text/plain');
check('json put/get', fn () => $disk->put("$root/data.json", json_encode(['a' => 1])) && $disk->json("$root/data.json") === ['a' => 1]);
check('prepend / append', function () use ($disk, $root) {
    $disk->append("$root/hello.txt", 'World', ' ');
    $disk->prepend("$root/hello.txt", 'Oh');

    return $disk->get("$root/hello.txt") === "Oh\nHello World";
});
check('copy', fn () => $disk->copy("$root/hello.txt", "$root/copy.txt") && $disk->get("$root/copy.txt") === "Oh\nHello World");
check('move', fn () => $disk->move("$root/copy.txt", "$root/moved/copy.txt") && $disk->missing("$root/copy.txt") && $disk->exists("$root/moved/copy.txt"));
check('makeDirectory / directoryExists', fn () => $disk->makeDirectory("$root/empty") && $disk->directoryExists("$root/empty"));
check('files', fn () => $disk->files($root) == ["$root/data.json", "$root/hello.txt"]);
check('allFiles', fn () => count($disk->allFiles($root)) === 3);
check('directories', fn () => $disk->directories($root) == ["$root/empty", "$root/moved"]);
// Flysystem's deep listing only reports folders that have a marker object,
// not ones implied by file paths. Real S3 behaves the same way.
check('allDirectories', fn () => $disk->allDirectories($root) == ["$root/empty"]);
check('visibility', function () use ($disk, $root) {
    $disk->setVisibility("$root/hello.txt", 'public');
    $public = $disk->getVisibility("$root/hello.txt");
    $disk->setVisibility("$root/hello.txt", 'private');

    return $public === 'public' && $disk->getVisibility("$root/hello.txt") === 'private';
});
check('put with public visibility', fn () => $disk->put("$root/pub.txt", 'x', 'public') && $disk->getVisibility("$root/pub.txt") === 'public');
check('url', fn () => $disk->url("$root/pub.txt") === "{$config['url']}/$root/pub.txt");
check('public url is fetchable', fn () => http('GET', $disk->url("$root/pub.txt"))[1] === 'x');
check('temporaryUrl', function () use ($disk, $root) {
    [$status, $body] = http('GET', $disk->temporaryUrl("$root/hello.txt", new DateTime('+5 minutes')));

    return $status === 200 && $body === "Oh\nHello World";
});
check('temporaryUrl with download name', function () use ($disk, $root) {
    $url = $disk->temporaryUrl("$root/hello.txt", new DateTime('+5 minutes'), ['ResponseContentDisposition' => 'attachment; filename=hi.txt']);
    [, , $headers] = http('GET', $url);

    return in_array('Content-Disposition: attachment; filename=hi.txt', $headers, true);
});
// Signatures are only checked with AUTH=verify (expiry always is).
if (json_decode(http('GET', "$endpoint/_api/info")[1], true)['verifyAuth'] ?? false) {
    check('temporaryUrl tampering is rejected', fn () => http('GET', str_replace('hello.txt', 'pub.txt', $disk->temporaryUrl("$root/hello.txt", new DateTime('+5 minutes'))))[0] === 403);
} else {
    echo "  - temporaryUrl tampering (skipped: server runs with AUTH=off)\n";
}
check('temporaryUploadUrl', function () use ($disk, $root) {
    ['url' => $url] = $disk->temporaryUploadUrl("$root/direct.txt", new DateTime('+5 minutes'));
    [$status] = http('PUT', $url, 'uploaded directly');

    return $status === 200 && $disk->get("$root/direct.txt") === 'uploaded directly';
});
check('writeStream / readStream', function () use ($disk, $root) {
    $in = fopen('php://temp', 'w+');
    fwrite($in, str_repeat('stream', 1000));
    rewind($in);
    $disk->writeStream("$root/stream.bin", $in);

    return stream_get_contents($disk->readStream("$root/stream.bin")) === str_repeat('stream', 1000);
});
// putFile() streams the file through writeStream(); the SDK switches to a
// multipart upload above 16 MB.
check('putFile-sized upload (20 MB, multipart)', function () use ($disk, $root) {
    $tmp = tempnam(sys_get_temp_dir(), 'big');
    $f = fopen($tmp, 'w');
    for ($i = 0; $i < 20; $i++) {
        fwrite($f, random_bytes(1024 * 1024));
    }
    fclose($f);
    $path = "$root/big.bin";
    $disk->writeStream($path, fopen($tmp, 'r'));
    $ok = $disk->size($path) === 20 * 1024 * 1024 && md5($disk->get($path)) === md5_file($tmp);
    unlink($tmp);

    return $ok;
});
check('delete', fn () => $disk->delete("$root/pub.txt") && $disk->missing("$root/pub.txt"));
check('delete many', fn () => $disk->delete(["$root/stream.bin", "$root/direct.txt"]) && $disk->missing("$root/stream.bin"));
check('get missing throws', function () use ($disk, $root) {
    try {
        $disk->get("$root/nope.txt");
    } catch (League\Flysystem\UnableToReadFile) {
        return true;
    }

    return false;
});
check('deleteDirectory', fn () => $disk->deleteDirectory($root) && $disk->allFiles($root) === []);

echo $failures ? "\n\033[31m$failures failed\033[0m\n" : "\n\033[32mAll checks passed\033[0m\n";
exit($failures ? 1 : 0);
