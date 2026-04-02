import pandas as pd

file_path = '/Users/zes/work/eino-examples/adk/multiagent/deep/playground/0fe1673e-653c-4201-bd99-01b3515c5a1e/questions.csv'
output_path = '/Users/zes/work/eino-examples/adk/multiagent/deep/playground/0fe1673e-653c-4201-bd99-01b3515c5a1e/first_column.csv'

df = pd.read_csv(file_path, header=None)

print("Original data shape:", df.shape)
print("
First 5 rows:")
print(df.head())

first_column = df.iloc[:, 0]

first_column_df = pd.DataFrame(first_column)

first_column_df.to_csv(output_path, index=False, header=False)

print("
Successfully extracted first column and saved to:", output_path)
print("First column has", len(first_column), "rows")
print("
First 10 values of first column:")
print(first_column.head(10))