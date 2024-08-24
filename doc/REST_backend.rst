************
REST Backend
************

Restic can interact with an HTTP Backend that respects the following REST API.

The following values are valid for ``{type}``:

* ``data``
* ``keys``
* ``locks``
* ``snapshots``
* ``index``
* ``config``

The placeholder ``{path}`` in this document is a path to the repository, so
that multiple different repositories can be accessed. The default path is
``/``. The path must end with a slash.

Version Negotiation
===================

The API version is selected via the ``Accept`` HTTP header in the client's request.
The following values are defined:

* ``application/vnd.x.restic.rest.v1`` or empty: Select API version 1 (deprecated)
* ``application/vnd.x.restic.rest.v2``: Select API version 2
* ``application/vnd.x.restic.rest.v3``: Select API version 3

Endpoints for which no version-specific behavior is described, behave identically for
all supported versions.

The server MUST return a "406 Not Acceptable" error if the requested API version
is not supported. Server implementations MUST set the ``Content-Type`` HTTP response
header if the called endpoint returns a JSON response and does not specify a
different value for that header. ``GET {path}/{type}/`` is currently the only such
endpoint. Clients MUST nevertheless include the ``Accept`` HTTP header in every request.

For compatibility with older server implementations, client implementations SHOULD be
prepared to handle the following behavior:

- Some server implementations return "400 Bad Request" when the client requests an
  API version other than 2. This should be treated equivalently to a "406 Not Acceptable"
  error.
- Some server implementations fall back to API version 1 when the client requests an
  unsupported API version. The client should check the ``Content-Type`` header of the
  response to detect this case. A missing / unexpected header value means API version 1.

HTTP error handling
===================

Most endpoints return "200 OK" if the requests was successful. If the description states
"an HTTP error otherwise", then it should be handled as follows. "404 Not Found" MUST
only be returned if the requested file does not exist. Unexpected errors like missing
file permissions or errors while storing data SHOULD be reported as
"500 Internal Server Error".

An unexpected HTTP method results in a "405 Method Not Allowed" error. Calling a not
existing endpoint results in a "404 Not Found" error.

Append-Only Backend
===================

The backend MAY restrict clients to only add new data, but not delete existing data.
This requires forbidding "DELETE" requests and "POST" requests that would overwrite
existing data. As an exception, overwriting a file is allowed if it already contains
the same data. In addition, clients are allowed to delete lock files.

POST {path}?create=true
=======================

This request is used to initially create a new repository. The server
responds with "200 OK" if the repository structure was created
successfully or already exists, otherwise an error is returned.

DELETE {path}
=============

Deletes the repository on the server side. The server responds with "200
OK" if the repository was successfully removed. If this function is not
implemented the server returns "501 Not Implemented", if it is
denied by the server it returns "403 Forbidden".

HEAD {path}/config
==================

Returns "200 OK" if the repository has a configuration, an HTTP error
otherwise. On success, the response SHOULD include the ``Content-Length`` header
with the size of the configuration file.

GET {path}/config
=================

Returns the content of the configuration file if the repository has a
configuration, an HTTP error otherwise.

On success, the response SHOULD include the ``Content-Length`` header
with the size of the configuration file.

``Content-Type``: binary/octet-stream

POST {path}/config
==================

Returns "200 OK" if the configuration of the request body has been
saved, an HTTP error otherwise.

DELETE {path}/config
====================

Returns "200 OK" if the configuration file has been deleted from the repository
or does not exist, an HTTP error otherwise.

GET {path}/{type}/
==================

API version 1
-------------

.. note

  Version 1 is deprecated.

Returns a JSON array containing the names of all the blobs stored for a given
type, example:

.. code:: json

    [
      "245bc4c430d393f74fbe7b13325e30dbde9fb0745e50caad57c446c93d20096b",
      "85b420239efa1132c41cea0065452a40ebc20c6f8e0b132a5b2f5848360973ec",
      "8e2006bb5931a520f3c7009fe278d1ebb87eb72c3ff92a50c30e90f1b8cf3e60",
      "e75c8c407ea31ba399ab4109f28dd18c4c68303d8d86cc275432820c42ce3649"
    ]

API version 2
-------------

Returns a JSON array containing an object for each file of the given type. The
objects have two keys: ``name`` for the file name, and ``size`` for the size in
bytes.

.. code:: json

    [
      {
        "name": "245bc4c430d393f74fbe7b13325e30dbde9fb0745e50caad57c446c93d20096b",
        "size": 2341058
      },
      {
        "name": "85b420239efa1132c41cea0065452a40ebc20c6f8e0b132a5b2f5848360973ec",
        "size": 2908900
      },
      {
        "name": "8e2006bb5931a520f3c7009fe278d1ebb87eb72c3ff92a50c30e90f1b8cf3e60",
        "size": 3030712
      },
      {
        "name": "e75c8c407ea31ba399ab4109f28dd18c4c68303d8d86cc275432820c42ce3649",
        "size": 2804
      }
    ]

If no entries exist, then an empty array object must be returned.

API version 3
-------------

API version 3 adds support for pagination and accepts the following query parameters:

- ``continue``: Opaque continuation token returned by the server in a
  previous response. On the first request, clients MUST omit this parameter or set it
  to an empty string.
- ``count``: Target count of items in the reply. The response from the server MAY exceed
  this count for performance reason.

Returns a JSON object containing the following keys:

- ``continue`` is an opaque string value that allows the client to continue listing
  the items by issuing further requests using the token. The token MUST be set in the
  reply if further data can be retrieved and MUST be an empty string or unset if no
  further data is available. The token MUST not be interpreted by clients in any way.
  Later requests using the continue token may return newer data.
- ``items`` is an array containing an object for each file of the given type. The
  objects have two keys: ``name`` for the file name, and ``size`` for the size in bytes.
  If no entries exist, then ``items`` is an empty array.

.. code:: json

    {
      "continue": "OPAQUE_TOKEN",
      "items": [
        {
          "name": "245bc4c430d393f74fbe7b13325e30dbde9fb0745e50caad57c446c93d20096b",
          "size": 2341058
        },
        {
          "name": "85b420239efa1132c41cea0065452a40ebc20c6f8e0b132a5b2f5848360973ec",
          "size": 2908900
        }
      ]
    }

HEAD {path}/{type}/{name}
=========================

Returns "200 OK" if the blob with the given name and type is stored in
the repository, an HTTP error otherwise. If the blob exists, the HTTP
header ``Content-Length`` is set to the file size.

GET {path}/{type}/{name}
========================

Returns the content of the blob with the given name and type if it is
stored in the repository, an HTTP error otherwise.

If the request specifies a partial read with a ``Range`` header field, then
the status code of the response is 206 instead of 200 and the response
only contains the specified range.

``Content-Type``: binary/octet-stream

POST {path}/{type}/{name}
=========================

Atomically saves the content of the request body as a blob with the given
name and type, an HTTP error otherwise. An upload MUST either complete
successfully or fail without modifying data on the server.

``Content-Type``: binary/octet-stream

The server should check that the SHA256 hash of the content of the request
body matches the file name. In case of a mismatch, return "400 Bad Request".

DELETE {path}/{type}/{name}
===========================

Returns "200 OK" if the blob with the given name and type has been
deleted from the repository or does not exist, an HTTP error otherwise.
