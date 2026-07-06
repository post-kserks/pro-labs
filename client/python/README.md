# VaultDB Python Client

Python TCP client for VaultDB protocol v2.

## Install

```bash
cd client/python
pip install -e .
```

## Usage

```python
from vaultdb import Client

# Basic query
with Client("localhost", 5432, "vdb_sk_your_token") as db:
    resp = db.query("SELECT * FROM users WHERE id = $1;", params=[42])
    print(resp["rows"])

# With explicit database
with Client("localhost", 5432, "vdb_sk_your_token") as db:
    resp = db.query("SELECT 1;", database="mydb")
    print(resp)

# Transactions
with Client("localhost", 5432, "vdb_sk_your_token") as db:
    db.begin()
    db.query("INSERT INTO users (name) VALUES ($1);", params=["bob"])
    db.commit()
```

## Manual connection

```python
db = Client("localhost", 5432, "vdb_sk_your_token")
info = db.connect()
print(info["server_version"])

resp = db.query("SELECT 1;")
db.close()
```

## Tests

```bash
pip install pytest
cd client/python
python -m pytest tests/ -v
```
