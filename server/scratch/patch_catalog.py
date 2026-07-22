import re

with open("internal/core/executor/types/catalog.go", "r") as f:
    content = f.read()

# AddViewPolicy debug
content = content.replace('def["policies"] = policies\n\treturn StoreObject', 'def["policies"] = policies\n\timport_fmt := "fmt"\n\t_ = import_fmt\n\tfmt.Printf("DEBUG: AddViewPolicy policies: %v\\n", policies)\n\treturn StoreObject')

# StoreObject debug
content = content.replace('defJSON, err := json.Marshal(definition)', 'defJSON, err := json.Marshal(definition)\n\tfmt.Printf("DEBUG: StoreObject marshaled JSON: %s\\n", string(defJSON))')

# LoadObject debug
content = content.replace('defJSON, _ := row[2].(string)\n\t\t\tif defJSON == ""', 'defJSON, _ := row[2].(string)\n\t\t\tfmt.Printf("DEBUG: LoadObject read JSON: %s\\n", defJSON)\n\t\t\tif defJSON == ""')

with open("internal/core/executor/types/catalog.go", "w") as f:
    f.write(content)
