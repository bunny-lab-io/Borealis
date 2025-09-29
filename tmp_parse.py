import ast, sys
with open('Data/Server/server.py','r',encoding='utf-8') as f:
    ast.parse(f.read())
print('OK')
