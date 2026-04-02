import pandas as pd
import os

os.chdir('/Users/zes/work/eino-examples/adk/multiagent/deep/playground/6c7fe631-ee85-43d0-8097-4f236c8b40d3')

df = pd.read_csv('questions.csv', header=None)

print("原始数据的形状:", df.shape)
print("
前5行数据:")
print(df.head())

first_column = df.iloc[:, 0]

print("
第一列数据:")
print(first_column.head(10))

first_column_df = pd.DataFrame(first_column)

first_column_df.to_csv('first_column.csv', index=False, header=False)

print(f"
成功！第一列数据已保存到 first_column.csv")
print(f"第一列共有 {len(first_column)} 行数据")

verification_df = pd.read_csv('first_column.csv', header=None)
print(f"
验证: first_column.csv 文件已创建，包含 {len(verification_df)} 行数据")
print("
first_column.csv 前10行内容:")
print(verification_df.head(10))