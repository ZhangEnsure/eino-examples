
import pandas as pd

file_path = '/Users/zes/work/eino-examples/adk/multiagent/deep/playground/c7058f72-19d3-40e8-9006-864963513150/questions.csv'
output_path = '/Users/zes/work/eino-examples/adk/multiagent/deep/playground/c7058f72-19d3-40e8-9006-864963513150/first_column.csv'

df = pd.read_csv(file_path, header=None)

print('原始数据形状:', df.shape)
print('原始数据列数:', len(df.columns))
print('\n前5行数据:')
print(df.head())

first_column = df.iloc[:, 0]

print('\n第一列数据样本:')
print(first_column.head(10))

first_column_df = pd.DataFrame(first_column)

first_column_df.to_csv(output_path, index=False, header=False)

print('\n成功提取第一列数据并保存到:', output_path)
print('第一列共有', len(first_column), '行数据')

verification_df = pd.read_csv(output_path, header=None)
print('\n验证: 新文件包含', len(verification_df), '行数据')
print('新文件前10行:')
print(verification_df.head(10))
